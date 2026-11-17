// Copyright (c) 2026 Costas Kirgoussios
//
// This software is licensed under the PolyForm Noncommercial License 1.0.0.
// You may obtain a copy of the License at:
// https://polyformproject.org/licenses/noncommercial/1.0.0/

package miner

import (
	"strings"
)

// The defaults of Config. They are the values the design settled on for the
// first release, measured on the sshd sample: depth 4 merged "Failed password"
// and "Accepted password" lines, depth 7 split them into too many patterns.
const (
	// DefaultMaxPatterns is the most patterns a miner makes. A message that
	// would make one more is counted in the catch-all instead.
	DefaultMaxPatterns = 1000
	// DefaultDepth is the depth of the tree, counting the root and the level of
	// the number of tokens: the path of a message is its number of tokens and
	// its first Depth-2 tokens (four tokens for the default).
	DefaultDepth = 6
	// DefaultMaxChildren is the most children a node of the tree has; more keys
	// go to the node's wildcard child.
	DefaultMaxChildren = 100
	// DefaultSimilarity is the share of positions that must be equal for a
	// message to join a cluster.
	DefaultSimilarity = 0.5
)

const (
	// wildcard is the text of a token of a pattern that varies, and the key of
	// the child of a node that takes every key the node has no child for.
	wildcard = "<*>"
	// catchAllText is the text of the catch-all pattern, pattern 0.
	catchAllText = "<other>"
)

// Config sets the limits of a Miner.
type Config struct {
	// MaxPatterns is the most clusters made; further new shapes go to the
	// catch-all.
	MaxPatterns int
	// Depth is the depth of the tree; see DefaultDepth. A Depth below 2 keys
	// no token.
	Depth int
	// MaxChildren is the most named children a node has; see DefaultMaxChildren.
	MaxChildren int
	// Similarity is the least share of equal positions with which a message
	// joins a cluster.
	Similarity float64
}

// DefaultConfig returns the configuration the program uses.
func DefaultConfig() Config {
	return Config{
		MaxPatterns: DefaultMaxPatterns,
		Depth:       DefaultDepth,
		MaxChildren: DefaultMaxChildren,
		Similarity:  DefaultSimilarity,
	}
}

// Pattern is what the miner learned about one shape of message. Text is the
// tokens of the template joined by one space; Slots has one entry for each
// value Match returns, in order. The catch-all has ID 0, Text "<other>" and no
// slots.
type Pattern struct {
	ID       int
	Text     string
	Slots    []SlotType
	CatchAll bool
}

// cluster is a template: the tokens that are the same in every message that
// joined it, and wildcard where they differ. Everything it keeps is a copy that
// shares no memory with a message.
type cluster struct {
	id     int          // 1 upward, in order of creation
	tokens []string     // the template; wildcard where the messages differ
	first  [][]SlotType // for each position, the types of the values of the first message's token; nil once the position is a wildcard
	fixed  int          // the number of positions that are not a wildcard
	slots  []SlotType   // set by Finish
}

// node is a node of the tree. Children are found by a direct lookup only: no
// map in this file is ever ranged over, so no order of a map reaches an output.
type node struct {
	kids     map[string]*node // children by key, at most MaxChildren
	star     *node            // the wildcard child
	clusters []*cluster       // for a leaf, in order of creation (so of ID)
}

// Miner is a Drain-style miner of patterns. It works in two passes: Learn for
// every message, then Finish, then Match for every message. A Miner is not
// safe for use by several goroutines while it learns; after Finish, Match only
// reads and may be called from several goroutines.
type Miner struct {
	cfg      Config
	root     map[int]*node // by number of tokens; looked up, never ranged over
	clusters []*cluster    // in order of creation: the ID of clusters[i] is i+1
	other    int           // messages that were counted in the catch-all
	finished bool
}

// New returns a Miner that uses c.
func New(c Config) *Miner {
	return &Miner{cfg: c, root: make(map[int]*node)}
}

// keyOf returns the key a token's Text has in the tree: the text itself, or
// wildcard when it holds a marker, a digit or a literal wildcard (so that the
// parts of a message that vary do not split the tree).
func keyOf(text string) string {
	for i := 0; i < len(text); i++ {
		if isDigit(text[i]) {
			return wildcard
		}
	}
	if strings.IndexByte(text, '<') < 0 {
		return text
	}
	if strings.Contains(text, wildcard) {
		return wildcard
	}
	for _, t := range [...]SlotType{Int, Dec, Hex, IPv4, IPv6, UUID} {
		if strings.Contains(text, t.Marker()) {
			return wildcard
		}
	}
	return text
}

// route returns the child of nd for key. A key that is not wildcard and has a
// child goes to it. When it has none and the node has fewer than max named
// children, create makes one; otherwise the key goes to the wildcard child
// (made when create is set and it is absent). With create unset, route reads
// only, and may return nil.
func (nd *node) route(key string, max int, create bool) *node {
	if key != wildcard {
		if c, ok := nd.kids[key]; ok {
			return c
		}
		if create && len(nd.kids) < max {
			if nd.kids == nil {
				nd.kids = make(map[string]*node)
			}
			c := &node{}
			// The key may be a substring of the message: copy it.
			nd.kids[strings.Clone(key)] = c
			return c
		}
	}
	if nd.star == nil && create {
		nd.star = &node{}
	}
	return nd.star
}

// walk returns the leaf of the tree for toks, or nil when create is unset and
// the path is missing. Learn and Match route the same way: a key with no child
// goes to the wildcard child.
func (m *Miner) walk(toks []Token, create bool) *node {
	nd := m.root[len(toks)]
	if nd == nil {
		if !create {
			return nil
		}
		nd = &node{}
		m.root[len(toks)] = nd
	}
	levels := m.cfg.Depth - 2
	if levels > len(toks) {
		levels = len(toks)
	}
	for i := 0; i < levels && nd != nil; i++ {
		nd = nd.route(keyOf(toks[i].Text), m.cfg.MaxChildren, create)
	}
	return nd
}

// fixedAt reports whether the cluster has a fixed token at pos that is the
// token t: the same Text and the same number of values. (A log may hold the
// text "<int>"; counting the values keeps the number of values a match returns
// equal to the number of slots.)
func (c *cluster) fixedAt(pos int, t Token) bool {
	return c.tokens[pos] != wildcard && c.tokens[pos] == t.Text && len(c.first[pos]) == len(t.Values)
}

// equalCount returns the number of positions at which the cluster has a fixed
// token equal to the message's, or -1 as soon as that number cannot reach
// floor. (A cluster that cannot reach floor is neither a better best nor able
// to take the message, so the answer -1 changes no outcome of Learn.)
func (c *cluster) equalCount(toks []Token, floor int) int {
	n := 0
	for i, t := range toks {
		if n+len(toks)-i < floor {
			return -1
		}
		if c.fixedAt(i, t) {
			n++
		}
	}
	if n < floor {
		return -1
	}
	return n
}

// joinFloor returns the least number of equal positions, out of n, that
// reaches the similarity.
func (m *Miner) joinFloor(n int) int {
	need := 0
	for need <= n && float64(need)/float64(n) < m.cfg.Similarity {
		need++
	}
	return need
}

// Learn is pass 1: it adds one message to the clusters. A message with no
// tokens is not learned. Past MaxPatterns clusters, no tree node is made, and
// a message that fits no cluster is counted in the catch-all. Learn panics
// with "miner: Learn after Finish" once Finish has been called.
func (m *Miner) Learn(msg string) {
	if m.finished {
		panic("miner: Learn after Finish")
	}
	toks := Scan(msg)
	if len(toks) == 0 {
		return
	}
	create := len(m.clusters) < m.cfg.MaxPatterns
	leaf := m.walk(toks, create)
	if leaf == nil {
		m.other++
		return
	}
	var best *cluster
	need := m.joinFloor(len(toks))
	bestN := need - 1
	for _, c := range leaf.clusters { // in ID order, so a tie keeps the lowest
		if n := c.equalCount(toks, bestN+1); n > bestN {
			best, bestN = c, n
		}
	}
	if best != nil { // its count reached the similarity
		best.join(toks)
		return
	}
	if !create {
		m.other++
		return
	}
	c := &cluster{
		id:     len(m.clusters) + 1,
		tokens: make([]string, len(toks)),
		first:  make([][]SlotType, len(toks)),
		fixed:  len(toks),
	}
	for i, t := range toks {
		c.tokens[i] = strings.Clone(t.Text)
		if len(t.Values) > 0 {
			types := make([]SlotType, len(t.Values))
			for j, v := range t.Values {
				types[j] = v.Type
			}
			c.first[i] = types
		}
	}
	leaf.clusters = append(leaf.clusters, c)
	m.clusters = append(m.clusters, c)
}

// join turns every position at which the message differs from the cluster into
// a wildcard.
func (c *cluster) join(toks []Token) {
	for i, t := range toks {
		if c.tokens[i] != wildcard && !c.fixedAt(i, t) {
			c.tokens[i] = wildcard
			c.first[i] = nil
			c.fixed--
		}
	}
}

// Finish ends pass 1 and returns the patterns in ID order, ID 1 upward, then
// the catch-all (ID 0) as the last element, whether or not a message fell into
// it. A pattern's slots are, for each token of its template, one Word slot when
// the token is a wildcard, and otherwise the types of the values of that token
// in the first message that made the cluster. They are counted from the values
// the scanner found, never from the text. A second call returns the same
// contents, in a new slice. No pattern ever changes after Finish.
func (m *Miner) Finish() []Pattern {
	if !m.finished {
		m.finished = true
		for _, c := range m.clusters {
			for i, tok := range c.tokens {
				if tok == wildcard {
					c.slots = append(c.slots, Word)
				} else {
					c.slots = append(c.slots, c.first[i]...)
				}
			}
		}
	}
	out := make([]Pattern, 0, len(m.clusters)+1)
	for _, c := range m.clusters {
		var slots []SlotType
		if len(c.slots) > 0 {
			slots = append(make([]SlotType, 0, len(c.slots)), c.slots...)
		}
		out = append(out, Pattern{ID: c.id, Text: strings.Join(c.tokens, " "), Slots: slots})
	}
	return append(out, Pattern{ID: 0, Text: catchAllText, CatchAll: true})
}

// matches reports whether every fixed token of the cluster equals the
// message's.
func (c *cluster) matches(toks []Token) bool {
	for i, t := range toks {
		if c.tokens[i] != wildcard && !c.fixedAt(i, t) {
			return false
		}
	}
	return true
}

// Match is pass 2: it returns the ID of the pattern of msg and one string for
// each slot of that pattern, in order. It walks the tree as Learn did; among
// the clusters of the leaf whose fixed tokens all equal the message's it takes
// the one with the most fixed tokens, the lowest ID on a tie. At each wildcard
// position the value is the whole token as written; at each other position it
// is the text of each typed value of the token; each is cut with CutValue. A
// message that fits no cluster, and a message with no tokens, gives ID 0 and
// no values. The values may share memory with msg. Match panics with
// "miner: Match before Finish" before Finish.
func (m *Miner) Match(msg string) (id int, values []string) {
	if !m.finished {
		panic("miner: Match before Finish")
	}
	toks := Scan(msg)
	if len(toks) == 0 {
		return 0, nil
	}
	leaf := m.walk(toks, false)
	if leaf == nil {
		return 0, nil
	}
	var best *cluster
	for _, c := range leaf.clusters { // in ID order, so a tie keeps the lowest
		if (best == nil || c.fixed > best.fixed) && c.matches(toks) {
			best = c
		}
	}
	if best == nil {
		return 0, nil
	}
	return best.id, best.values(toks)
}

// values returns the values of a message that matches the cluster.
func (c *cluster) values(toks []Token) []string {
	var out []string
	if len(c.slots) > 0 {
		out = make([]string, 0, len(c.slots))
	}
	for i, t := range toks {
		if c.tokens[i] == wildcard {
			out = append(out, CutValue(t.Raw))
			continue
		}
		for _, v := range t.Values {
			out = append(out, CutValue(v.Text))
		}
	}
	return out
}
