# ft CLI

A CLI for a live Forethought account. Before running any command that
writes, publishes or previews, read [AGENTS.md](AGENTS.md): it has the
approval rules (reads are free; `--apply` and `--confirm` need the user's OK
per change), the standard workflows, and FT behaviour that isn't obvious.

- Commands and setup: [README.md](README.md)
- Status of each command and what's confirmed live: [SPEC.md](SPEC.md)
- Build and test: `go build -o ft . && go vet ./... && go test ./...`
