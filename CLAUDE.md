# diskmap

Go (stdlib only) disk usage scanner + embedded loopback web UI (`web/`, plain
ES module JS, no build step). Packages: `scan` (parallel walker, compact
tree), `trash` (freedesktop Trash / permanent delete), `server` (HTTP API,
loopback + token guards). `main.go`/`serve.go` wire the CLI.

- Gate: `scripts/check.sh` (gofmt, go vet, go test -race, selftest).
- Benchmark: `./bench.sh [--du] [ROOT...]` outside CPU caps (systemd-run).
- Tests never touch real data: trash tests set `XDG_DATA_HOME` to a temp dir.

## Commands
- deploy: `git -C ~/src/diskmap pull --ff-only && cd ~/src/diskmap && go build -trimpath -o ~/.local/bin/diskmap . && ~/.local/bin/diskmap --selftest`
