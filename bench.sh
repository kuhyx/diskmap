#!/bin/bash
# Time a full diskmap scan of the given roots (default / and /home) and fail
# when it takes 60 s or more. With --du, also compare the biggest top-level
# folder of the first root against `du -x -s -B1` (must agree within 1 %).
#
# Run it outside resource caps: a CPU quota slows the walk and the number
# would not describe the tool. Page-cache state matters a lot; drop caches
# first (`sync; echo 3 | sudo tee /proc/sys/vm/drop_caches`) for a cold run.
set -euo pipefail

readonly LIMIT_MS=60000
cd "$(dirname "$0")"

check_du=0
roots=()
for a in "$@"; do
	case "$a" in
	--du) check_du=1 ;;
	*) roots+=(--root "$a") ;;
	esac
done

command -v jq >/dev/null || { echo "bench.sh needs jq" >&2; exit 1; }
tmp="$(mktemp -d)"
trap 'rm -rf "$tmp"' EXIT

go build -o "$tmp/diskmap" .
start=$(date +%s%N)
"$tmp/diskmap" --scan-only --top 5 "${roots[@]}" >"$tmp/out.json"
wall_ms=$(( ($(date +%s%N) - start) / 1000000 ))

jq -r '.progress | "entries: \(.files + .dirs)  bytes: \(.bytes)  scan: \(.ms) ms"' "$tmp/out.json"
jq -r '.roots[] | "root \(.path): \(.size) bytes, \(.count) entries"' "$tmp/out.json"
echo "wall-clock: ${wall_ms} ms (limit ${LIMIT_MS} ms)"

if (( check_du )); then
	read -r path size < <(jq -r '.top | to_entries[0].value[] | select(.dir) | "\(.path) \(.size)"' "$tmp/out.json" | head -1)
	du_size=$(du -x -s -B1 "$path" 2>/dev/null | cut -f1 || true)
	echo "biggest top-level folder $path: diskmap $size, du $du_size"
	awk -v a="$size" -v b="$du_size" 'BEGIN { d = a - b; if (d < 0) d = -d; exit !(b > 0 && d / b <= 0.01) }' \
		|| { echo "FAIL: differs by more than 1 %" >&2; exit 1; }
fi
(( wall_ms < LIMIT_MS )) || { echo "FAIL: scan took ${wall_ms} ms" >&2; exit 1; }
