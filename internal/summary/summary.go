// Copyright (c) 2026 Costas Kirgoussios
//
// This software is licensed under the PolyForm Noncommercial License 1.0.0.
// You may obtain a copy of the License at:
// https://polyformproject.org/licenses/noncommercial/1.0.0/

package summary

import (
	"context"
	"errors"
	"fmt"
	"sort"

	"github.com/ckir/aigolog/internal/store"
)

// MinBudget is the smallest budget Build accepts, in bytes of rendered text. At
// this size the sources block (at most 16 lines of at most 100 bytes, and one
// line counting the rest) and the miner block (at most 400 bytes) always fit.
const MinBudget = 4096

// ErrBudgetTooSmall is returned by Build for a Budget below MinBudget, so that
// len(Render(s)) <= Budget holds for every budget Build accepts.
var ErrBudgetTooSmall = errors.New("the summary budget is below the minimum of 4096 bytes")

// patternsShare is the share of the budget, in percent, that the patterns block
// may use at most.
const patternsShare = 60

// timePatterns is how many of the listed patterns get a time block of their own.
const timePatterns = 3

// Options set the size and the contents of the summary and the digest.
type Options struct {
	// Budget is the most bytes Render may give for the summary; at least
	// MinBudget.
	Budget int `json:"budget"`
	// DigestGroups is how many of the largest patterns the digest lists.
	DigestGroups int `json:"digest_groups"`
	// SlotPatterns is how many of the listed patterns get a slot block.
	SlotPatterns int `json:"slot_patterns"`
	// TopValues is how many of the most frequent values a slot block lists for
	// each slot.
	TopValues int `json:"top_values"`
	// Buckets is the most intervals of a time block; 0 or less leaves the time
	// blocks out.
	Buckets int `json:"buckets"`
}

// DefaultOptions returns the options the program uses. The budget is
// provisional: it is set by measurement in sub-project C.
func DefaultOptions() Options {
	return Options{Budget: 16384, DigestGroups: 10, SlotPatterns: 10, TopValues: 5, Buckets: 12}
}

// Omitted says what the list of patterns left out: the number of patterns, the
// catch-all counted as one when it was left out, and the records they hold. A
// pattern with no record is not a visible pattern and is never counted.
type Omitted struct {
	Patterns int   `json:"patterns"`
	Records  int64 `json:"records"`
}

// Block is one part of the summary, with an identifier by which a finding can
// point at it. Only the fields of its Kind are set:
//
//	sources   Sources: every source of the run (Render lists the first 16)
//	miner     Miner
//	patterns  Patterns: the listed patterns, most frequent first
//	slots     Pattern and Patterns (that one pattern), Slots
//	time      Pattern (-1 for all records) and Buckets; for a pattern also
//	          Patterns (that one pattern)
type Block struct {
	// ID is S1, S2, ... in order of the blocks.
	ID string `json:"id"`
	// Kind is sources, miner, patterns, slots or time.
	Kind     string              `json:"kind"`
	Sources  []store.SourceInfo  `json:"sources,omitempty"`
	Miner    *store.MinerReport  `json:"miner,omitempty"`
	Patterns []store.PatternStat `json:"patterns,omitempty"`
	Slots    []store.SlotStat    `json:"slots,omitempty"`
	// Pattern is the pattern a slots or time block is about; -1 for the time
	// block of all records. It is set (not omitted) for every block, 0 being
	// the catch-all.
	Pattern int64          `json:"pattern"`
	Buckets []store.Bucket `json:"buckets,omitempty"`
}

// Summary is the blocks, in the order sources, miner, patterns, slots, time,
// and what the list of patterns left out.
type Summary struct {
	Blocks  []Block `json:"blocks"`
	Omitted Omitted `json:"omitted"`
}

// Digest is the head of the summary: the largest groups for a person to read.
type Digest struct {
	// Records is the number of all records; Patterns the number of mined
	// patterns (the catch-all is not one).
	Records  int64 `json:"records"`
	Patterns int   `json:"patterns"`
	// Groups are the largest patterns by records and then id; the catch-all is
	// one of them only when it has records. Each Share is the store's.
	Groups []store.PatternStat `json:"groups"`
}

// builder holds the summary that Build is making.
type builder struct {
	budget  int
	blocks  []Block
	omitted Omitted
}

// fits reports whether the summary with blocks and omitted renders within the
// budget. This is the one check of the size rule: it renders.
func (b *builder) fits(blocks []Block, omitted Omitted) bool {
	return len(Render(Summary{Blocks: blocks, Omitted: omitted})) <= b.budget
}

// with returns the blocks of b and blk after them, with the identifier blk
// takes there.
func (b *builder) with(blk Block) []Block {
	blk.ID = fmt.Sprintf("S%d", len(b.blocks)+1)
	out := make([]Block, 0, len(b.blocks)+1)
	return append(append(out, b.blocks...), blk)
}

// add appends blk when the summary with it still fits the budget, and reports
// whether it did. A block that does not fit is left out whole.
func (b *builder) add(blk Block) bool {
	cand := b.with(blk)
	if !b.fits(cand, b.omitted) {
		return false
	}
	b.blocks = cand
	return true
}

// withRecords returns the patterns that hold at least one record, in order. A
// mined pattern of no record is possible (the second pass never chose it); it
// is not visible, so it is neither listed nor counted as left out.
func withRecords(ps []store.PatternStat) []store.PatternStat {
	out := make([]store.PatternStat, 0, len(ps))
	for _, p := range ps {
		if p.Records > 0 {
			out = append(out, p)
		}
	}
	return out
}

// fail returns the error of the context when it is the cause, and err as it is
// otherwise (store.ErrClosed stays store.ErrClosed).
func fail(ctx context.Context, err error) (Summary, Digest, error) {
	if cerr := ctx.Err(); cerr != nil {
		return Summary{}, Digest{}, cerr
	}
	return Summary{}, Digest{}, err
}

// Build makes the summary and the digest of db. It only reads, and makes
// O(SlotPatterns + 4) queries (SourceInfos, MinerReport and PatternStats once,
// SlotStats once for each slot block, TimeBuckets for all records and for at
// most three patterns), never one for a value or a record.
//
// Blocks are added in the order sources, miner, patterns, slots, time. Each is
// added only when the render of the summary with it stays within the budget,
// except the sources and the miner blocks, which are bounded so that they fit
// every budget Build accepts.
func Build(ctx context.Context, db *store.DB, o Options) (Summary, Digest, error) {
	if o.Budget < MinBudget {
		return Summary{}, Digest{}, ErrBudgetTooSmall
	}
	if err := ctx.Err(); err != nil {
		return Summary{}, Digest{}, err
	}
	srcs, err := db.SourceInfos(ctx)
	if err != nil {
		return fail(ctx, err)
	}
	rep, err := db.MinerReport(ctx)
	if err != nil {
		return fail(ctx, err)
	}
	stats, err := db.PatternStats(ctx, 0)
	if err != nil {
		return fail(ctx, err)
	}
	visible := withRecords(stats)

	b := &builder{budget: o.Budget}
	b.blocks = b.with(Block{Kind: "sources", Sources: srcs})
	b.blocks = b.with(Block{Kind: "miner", Miner: &rep})
	if !b.fits(b.blocks, Omitted{}) {
		return Summary{}, Digest{}, errors.New("the sources and miner blocks do not fit the minimum budget")
	}

	k := b.addPatterns(visible)
	listed := visible[:k]
	for i := 0; i < len(listed) && i < o.SlotPatterns; i++ {
		p := listed[i]
		if len(p.Slots) == 0 {
			continue
		}
		ss, err := db.SlotStats(ctx, p.ID, o.TopValues)
		if err != nil {
			return fail(ctx, err)
		}
		if len(ss) == 0 {
			continue
		}
		b.add(Block{Kind: "slots", Pattern: p.ID, Patterns: []store.PatternStat{p}, Slots: ss})
	}

	if o.Buckets > 0 {
		bs, err := db.TimeBuckets(ctx, -1, o.Buckets)
		if err != nil {
			return fail(ctx, err)
		}
		if len(bs) > 0 {
			b.add(Block{Kind: "time", Pattern: -1, Buckets: bs})
		}
		for i := 0; i < len(listed) && i < timePatterns; i++ {
			p := listed[i]
			if !p.HasTimes {
				continue
			}
			bs, err := db.TimeBuckets(ctx, p.ID, o.Buckets)
			if err != nil {
				return fail(ctx, err)
			}
			if len(bs) > 0 {
				b.add(Block{Kind: "time", Pattern: p.ID, Patterns: []store.PatternStat{p}, Buckets: bs})
			}
		}
	}

	groups := make([]store.PatternStat, 0, max(0, min(o.DigestGroups, len(visible))))
	for i := 0; i < len(visible) && i < o.DigestGroups; i++ {
		groups = append(groups, visible[i])
	}
	// The store orders the patterns so already; the sort states the contract of
	// the digest here: the most records first, a tie by the smaller id.
	sort.SliceStable(groups, func(i, j int) bool {
		if groups[i].Records != groups[j].Records {
			return groups[i].Records > groups[j].Records
		}
		return groups[i].ID < groups[j].ID
	})
	return Summary{Blocks: b.blocks, Omitted: b.omitted}, Digest{Records: rep.Records, Patterns: rep.Patterns, Groups: groups}, nil
}

// addPatterns adds the patterns block, listing the first k of the patterns for
// the largest k that fits, and sets the omitted count for the rest. The block
// may use at most patternsShare percent of the budget, and the summary with it
// must fit the budget. It returns k, 0 when no block was added (there is
// nothing to list, or not even the heading fits).
func (b *builder) addPatterns(visible []store.PatternStat) int {
	if len(visible) == 0 {
		return 0
	}
	// restAfter is what the list leaves out when it lists the first k.
	restAfter := func(k int) Omitted {
		o := Omitted{Patterns: len(visible) - k}
		for _, p := range visible[k:] {
			o.Records += p.Records
		}
		return o
	}
	blockFor := func(k int) Block {
		return Block{Kind: "patterns", Patterns: visible[:k:k]}
	}
	ok := func(k int) bool {
		om := restAfter(k)
		cand := b.with(blockFor(k))
		// The block alone is measured as if its times named the zone, which
		// is the longest it can be.
		own := render(Summary{Blocks: cand[len(cand)-1:], Omitted: om}, true)
		if int64(len(own))*100 > int64(b.budget)*patternsShare {
			return false
		}
		return b.fits(cand, om)
	}
	if !ok(0) {
		return 0
	}
	// The largest k that fits, by bisection: lo fits, hi does not (or is one
	// past the end). Adding a pattern adds a line of tens of bytes and the line
	// that counts the rest changes by a few, so what fits is a prefix.
	lo, hi := 0, len(visible)+1
	if ok(len(visible)) {
		lo = len(visible)
	} else {
		hi = len(visible)
		for hi-lo > 1 {
			mid := lo + (hi-lo)/2
			if ok(mid) {
				lo = mid
			} else {
				hi = mid
			}
		}
	}
	b.blocks = b.with(blockFor(lo))
	b.omitted = restAfter(lo)
	return lo
}
