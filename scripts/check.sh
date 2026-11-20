#!/bin/bash
# Repo gate: formatting, vet, race-enabled tests and the binary's selftest.
set -euo pipefail
cd "$(dirname "$0")/.."
unformatted="$(gofmt -l .)"
[[ -z "$unformatted" ]] || { echo "gofmt needed: $unformatted" >&2; exit 1; }
go vet ./...
go test -race ./...
tmp="$(mktemp -d)"
trap 'rm -rf "$tmp"' EXIT
go build -o "$tmp/diskmap" .
"$tmp/diskmap" --selftest
