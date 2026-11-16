# A1: the program builds, with every dependency in place

Date: 2026-11-16. Commit: `20ff2af`. Workflow run: [number 1](https://github.com/ckir/aigolog/actions/runs/43802284658) (run id 43802284658), on pull request 2. All three jobs succeeded.

| Target | Cross-built on ubuntu-24.04 by `just build` | Built, tested and run on its own kind of machine |
|---|---|---|
| linux/amd64 | yes | yes, ubuntu-24.04 |
| linux/arm64 | yes | no |
| windows/amd64 | yes | yes, windows-2025 |
| windows/arm64 | yes | yes, windows-11-arm |

What `aigolog version` printed on each machine that ran it ("Run the program" step):

ubuntu-24.04:

```
aigolog 0.1.0-dev
go go1.27.4 linux/amd64
sqlite 3.54.1
```

windows-2025:

```
aigolog 0.1.0-dev
go go1.27.4 windows/amd64
sqlite 3.54.1
eventlog api ok
```

windows-11-arm:

```
aigolog 0.1.0-dev
go go1.27.4 windows/arm64
sqlite 3.54.1
eventlog api ok
```

In "The gate" step, `go test` printed `ok` for `internal/cli`, `internal/exitcode`, `internal/repocheck` and `internal/store` on all three machines, and for `internal/winlog` on the two Windows machines (on Linux that package has no test files).

Dependencies: `modernc.org/sqlite` v1.62.0 and `golang.org/x/sys` v0.49.0, both with `CGO_ENABLED=0`. The Windows Event Log API was found from Go on both Windows machines, by the test `TestEventLogAPIIsReachable` and by the program itself (its fourth line).

`just` 1.58.0 was installed on each machine by `scripts/ci/install-just.sh`, with its download checked against the pinned checksum (`OK` on all three).

Not shown by this run: the `go 1.26.0` floor in `go.mod` (the workflow builds with Go 1.27.4 only), and linux/arm64 running on its own kind of machine.
