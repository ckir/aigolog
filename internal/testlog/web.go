// Copyright (c) 2026 Costas Kirgoussios
//
// This software is licensed under the PolyForm Noncommercial License 1.0.0.
// You may obtain a copy of the License at:
// https://polyformproject.org/licenses/noncommercial/1.0.0/

package testlog

import (
	"fmt"
	"math/rand"
	"strconv"
	"strings"
	"time"
)

// WebTruth is what a web log holds, counted by the generator from the lines it
// wrote: the number of requests, the distinct paths, and the client with the
// most requests (the smallest address when two have as many), with its
// requests and its distinct paths.
type WebTruth struct {
	Requests, DistinctPaths int
	Client                  string
	ClientRequests          int
	ClientPaths             int
}

// webStart is the time of the first line. The stamp has a zone, so it is exact.
var webStart = time.Date(2026, time.November, 16, 8, 15, 22, 0, time.UTC)

// webPages are the paths that the ordinary clients repeat. Each holds a digit:
// the miner takes the third token of a message as a key of its tree unless the
// token holds a digit, and a path that is a key is a part of the pattern, not a
// value in it.
var webPages = []string{
	"/index2.html", "/static/app.v2.css", "/static/app.v2.js", "/api/v1/items", "/api/v1/users", "/img/logo2.png",
}

var (
	webDirs  = []string{"admin", "cgi-bin", "backup", "wp-admin", "phpmyadmin", "old", "test"}
	webFiles = []string{"login", "index", "setup", "config", "shell", "upload"}
	webExts  = []string{"php", "asp", "cgi", "bak"}
)

// Web makes a web log in the common log format with the time first, and its
// truth: one client probes many different paths (and asks again for some),
// ten others repeat a few.
func Web(seed int64, lines int) ([]byte, WebTruth) {
	var truth WebTruth
	if lines <= 0 {
		return nil, truth
	}
	rng := rand.New(rand.NewSource(seed))
	prober := "203.0.113." + strconv.Itoa(1+rng.Intn(254))
	var others []string
	for _, n := range rng.Perm(254)[:10] {
		others = append(others, "198.51.100."+strconv.Itoa(n+1))
	}
	type who struct {
		n     int
		paths map[string]bool
	}
	clients := map[string]*who{}
	paths := map[string]bool{}
	var probed []string // the paths the prober has asked for, in order
	var b strings.Builder
	now := webStart
	for no := 1; no <= lines; no++ {
		if no > 1 {
			now = advance(now, rng.Intn(3))
		}
		var client, path string
		status, size := 200, 200+rng.Intn(60000)
		if rng.Intn(100) < 45 {
			client = prober
			if len(probed) > 0 && rng.Intn(100) < 10 {
				// The prober asks again for one path in ten, so that its requests
				// are more than its distinct paths.
				path = probed[rng.Intn(len(probed))]
			} else {
				path = fmt.Sprintf("/%s/%s%d.%s", webDirs[rng.Intn(len(webDirs))], webFiles[rng.Intn(len(webFiles))], no, webExts[rng.Intn(len(webExts))])
				probed = append(probed, path)
			}
			status, size = 404, 100+rng.Intn(900)
			if rng.Intn(50) == 0 {
				status = 403
			}
		} else {
			client = others[rng.Intn(len(others))]
			path = webPages[rng.Intn(len(webPages))]
			status = [...]int{200, 200, 200, 304, 404}[rng.Intn(5)]
		}
		w := clients[client]
		if w == nil {
			w = &who{paths: map[string]bool{}}
			clients[client] = w
		}
		w.n++
		w.paths[path] = true
		paths[path] = true
		fmt.Fprintf(&b, "%s %s \"GET %s HTTP/1.1\" %d %d\n", now.Format(clfLayout), client, path, status, size)
	}
	truth.Requests, truth.DistinctPaths = lines, len(paths)
	for c, w := range clients {
		if w.n > truth.ClientRequests || (w.n == truth.ClientRequests && c < truth.Client) {
			truth.Client, truth.ClientRequests, truth.ClientPaths = c, w.n, len(w.paths)
		}
	}
	return []byte(b.String()), truth
}
