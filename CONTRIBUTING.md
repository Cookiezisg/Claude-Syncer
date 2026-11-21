# Contributing

Bug reports and pull requests are welcome.

## Before you start

Claude-Syncer edits data people care about, in a format Claude Desktop does
not document. Two rules follow from that:

- **Behaviour first, verified.** If a change relies on how Claude Desktop
  behaves, say how you verified it, and record it in
  [docs/desktop-internals.md](docs/desktop-internals.md).
- **Stop rather than guess.** When the code meets something it does not
  recognise, it should leave it alone and report it, not repair it.

[docs/design.md](docs/design.md) explains how a sync pass decides what to write.

## Working on it

```sh
make test      # go test -race ./...
make lint      # gofmt + go vet
make build
./claude-syncer sync --dry-run
```

Engine changes need a scenario in
[internal/engine/scenarios_test.go](internal/engine/scenarios_test.go). The
tests build fake Desktop folders in a temporary directory and never touch
real data. Check that the new test fails without your change.

`--dry-run` against your own data is the best final check: it prints every
write it would make and makes none.

## Releasing

See [docs/releasing.md](docs/releasing.md).
