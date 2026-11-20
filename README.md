# diskmap

A fast [QDirStat](https://github.com/shundhammer/qdirstat) alternative with no
Qt: one static Go binary that scans your disks in parallel and opens a web UI
on `127.0.0.1`. The UI lists the biggest folders and files, draws a treemap,
and deletes what you select (to the Trash by default).

## Build and run

Needs Go 1.27+. No other dependencies (stdlib only, UI is embedded).

```sh
go build -o ~/.local/bin/diskmap .
diskmap                       # scans / and /home, opens a browser tab
diskmap --root ~/Downloads    # scan one folder
```

`diskmap` prints the URL (`http://127.0.0.1:<port>/`) and opens it with
`xdg-open`. Stop it with Ctrl-C.

## Flags

| Flag | Default | Meaning |
| --- | --- | --- |
| `--root DIR` | `/` and `/home` | Directory to scan. Repeat for more. A root that another root's walk reaches anyway (e.g. `/home` on the same filesystem as `/`) is scanned once. |
| `--cross` | off | Also enter other mounted filesystems. Pseudo filesystems (proc, sysfs, devtmpfs, tmpfs, cgroup, overlay, ...) are always skipped. |
| `--port N` | `0` (any free) | Port on 127.0.0.1. |
| `--no-browser` | off | Do not open a browser tab. |
| `--scan-only` | off | Scan, print stats as JSON, exit (for benchmarks). `--top N` adds each root's N biggest entries. |
| `--selftest` | off | Scan a temp fixture through the HTTP API and trash a temp file; exits non-zero on failure. |

## Using the UI

- **Header:** up button, breadcrumb (click any part), scan progress (files,
  bytes, time; a spinner turns into a check mark when done).
- **List (left):** the current folder's entries, biggest first, with size,
  a size bar and the entry count. Click a folder to open it; click a file or
  its checkbox to select it.
- **Treemap (right):** rectangles sized by disk usage, three levels deep,
  colored by file extension. Hover shows path and size; click a folder to
  open it; Ctrl-click selects.
- **Delete:** with items selected, the header shows their count, total size
  and a delete button. It opens a confirm step listing every path and the
  total.
  - The amber trash button moves them to the Trash.
  - The red flame button deletes permanently. It needs a second press: the
    first press turns the dialog red and hides the trash button.
  - The close button cancels; nothing is touched.
  - Afterwards a result list marks each path ✓ or ⚠ with the error. One
    failure never stops the other items. The tree updates from disk.

## Trash vs permanent

- **Trash (default):** follows the freedesktop.org Trash spec, so file
  managers can restore the items. Same filesystem as your home:
  `~/.local/share/Trash` (`$XDG_DATA_HOME/Trash`). Another filesystem:
  `<mountpoint>/.Trash/<uid>` if an admin set it up, else
  `<mountpoint>/.Trash-<uid>`. diskmap never copies across filesystems; if
  no trash can be made there, that item fails.
- **Permanent:** `rm -rf` semantics for the selected path. Symlinks are
  removed, never followed. If part of a folder cannot be removed, the rest
  is gone and the item is reported as failed.

## What is counted

Sizes are allocated disk blocks (`st_blocks * 512`), the same as `du`.
Hardlinked files count once. Folders you cannot read are marked with a lock
and count only their own block. With the default flags the result matches
`du -x -s -B1`.

## Safety

The server listens on 127.0.0.1 only. Every request needs a loopback `Host`
header (blocks DNS rebinding), API calls need a random per-run token, and
delete requests reject foreign `Origin` headers. Delete refuses a scan root,
anything outside the roots and anything not in the scanned tree.

## Benchmark

```sh
./bench.sh            # times / and /home, fails at >= 60 s
./bench.sh --du       # also checks the biggest top-level folder against du -x -s
```

Run it outside CPU quotas. Measured on kuhy's PC (16 threads, NVMe ext4,
2.2 TB and 18.4 M entries on `/`, `/home` on the same filesystem):

| Run (2026-11-20) | Cache | Wall-clock | Entries |
| --- | --- | --- | --- |
| 1 | partly cold (first run) | 22.6 s | 18.45 M |
| 2 | dropped, then a few minutes of other load | 19.4 s | 18.40 M |
| 3 | warm | 21.0 s | 17.75 M |
| 4 | dropped right before (`drop_caches=3`) | 20.6 s | 17.75 M |

Median about 20.7 s, range 19.4 to 22.6 s over 4 runs. Cold and warm runs
take about the same time: the limit is kernel time in `getdents`/`fstatat`
(about 270 of 300 CPU-seconds are system time), not disk reads. Peak RSS is
about 1.7 GB. The biggest top-level folder, `/home`, came out at
1,794,943,549,440 bytes against `du -x -s -B1` at 1,795,196,551,168
(0.014 % apart; files changed between the two runs).

## Development

```sh
scripts/check.sh                         # gofmt, vet, go test -race, selftest
go build -o /tmp/diskmap . && node e2e/e2e.mjs /tmp/diskmap shots/   # UI end-to-end in headless chromium
```
