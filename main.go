// Command diskmap scans disks in parallel and serves a loopback web UI that
// lists the biggest folders and files, draws a treemap and deletes the
// selection (to the freedesktop Trash by default).
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"strings"

	"github.com/kuhyx/diskmap/scan"
)

type rootList []string

func (r *rootList) String() string     { return strings.Join(*r, ",") }
func (r *rootList) Set(v string) error { *r = append(*r, v); return nil }

func main() {
	var roots rootList
	flag.Var(&roots, "root", "directory to scan (repeatable; default / and /home)")
	cross := flag.Bool("cross", false, "also scan other mounted filesystems (pseudo filesystems stay skipped)")
	port := flag.Int("port", 0, "port on 127.0.0.1 (0 = pick a free one)")
	noBrowser := flag.Bool("no-browser", false, "do not open a browser tab")
	scanOnly := flag.Bool("scan-only", false, "scan, print stats as JSON and exit (benchmarking)")
	top := flag.Int("top", 0, "with --scan-only: also print the N biggest entries of each root")
	selftest := flag.Bool("selftest", false, "run a self-check on a temp fixture and exit non-zero on failure")
	flag.Parse()
	if len(roots) == 0 {
		roots = rootList{"/", "/home"}
	}
	if *selftest {
		if err := selfTest(); err != nil {
			fmt.Fprintln(os.Stderr, "selftest failed:", err)
			os.Exit(1)
		}
		fmt.Println("selftest ok")
		return
	}
	opts := scan.Options{Roots: roots, Cross: *cross}
	if *scanOnly {
		s := scan.Start(opts)
		t := s.Wait()
		out := map[string]any{"progress": s.Progress(), "roots": t.Children(t.Top)}
		if *top > 0 {
			biggest := map[string][]*scan.Entry{}
			for _, r := range t.Children(t.Top) {
				kids := t.Children(r)
				if len(kids) > *top {
					kids = kids[:*top]
				}
				biggest[r.Path] = kids
			}
			out["top"] = biggest
		}
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		_ = enc.Encode(out)
		return
	}
	if err := serve(opts, *port, !*noBrowser); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
