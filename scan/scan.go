// Package scan walks directory trees in parallel and keeps a compact,
// size-aggregated tree of the result. Sizes are allocated bytes
// (st_blocks*512), hardlinked inodes count once, and mount boundaries are
// respected the way `du -x` does, so totals match `du -x -s -B1`.
package scan

import (
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"unsafe"
)

// Options controls a scan.
type Options struct {
	Roots   []string // directories to scan; nested roots are merged
	Cross   bool     // enter other (non-pseudo) filesystems
	Workers int      // parallel directory readers; 0 = 4*NumCPU
}

// node is one directory. Files are kept inline as compact records whose
// names share one byte buffer, so millions of files stay cheap in RAM.
type node struct {
	name   string
	parent *node
	size   int64 // own blocks, then (after aggregation) the whole subtree
	count  int64 // entries in the subtree, including itself
	dirs   []*node
	files  []file
	names  []byte
	err    bool // could not be read
	mount  bool // mountpoint that was not entered
}

type file struct {
	off, n uint32
	size   int64
}

func (n *node) fileName(f file) string { return string(n.names[f.off : f.off+f.n]) }

// Entry is a read-only snapshot of a directory or file in the tree.
type Entry struct {
	Name  string `json:"name"`
	Path  string `json:"path"`
	Size  int64  `json:"size"`
	Count int64  `json:"count"`
	IsDir bool   `json:"dir"`
	Err   bool   `json:"err,omitempty"`
	Mount bool   `json:"mount,omitempty"`

	dir  *node // the directory itself, or the parent of a file
	file int   // index in dir.files, -1 for a directory
}

// Progress is a live snapshot of a running scan.
type Progress struct {
	Files int64 `json:"files"`
	Dirs  int64 `json:"dirs"`
	Bytes int64 `json:"bytes"`
	Done  bool  `json:"done"`
	// Millis is the wall-clock scan time so far.
	Millis int64 `json:"ms"`
}

// Tree is the scan result. Its methods are safe for concurrent use.
type Tree struct {
	mu   sync.RWMutex
	top  *node
	opts Options
	Top  *Entry
}

// Scan walks opts.Roots and returns the finished tree.
func Scan(opts Options) *Tree { return Start(opts).Wait() }

// Scanner is a scan in progress.
type Scanner struct {
	opts    Options
	mnt     mounts
	files   atomic.Int64
	dirs    atomic.Int64
	bytes   atomic.Int64
	done    atomic.Bool
	started int64
	elapsed atomic.Int64
	result  chan *Tree

	links [64]linkShard

	qmu     sync.Mutex
	qcond   *sync.Cond
	queue   []job
	pending int
}

type job struct {
	n    *node
	path string
	dev  uint64
}

type linkKey struct{ dev, ino uint64 }

type linkShard struct {
	sync.Mutex
	seen map[linkKey]struct{}
}

// Start begins a scan in the background.
func Start(opts Options) *Scanner {
	if opts.Workers <= 0 {
		opts.Workers = 4 * runtime.NumCPU()
	}
	s := &Scanner{opts: opts, mnt: readMounts(), result: make(chan *Tree, 1), started: nowMillis()}
	s.qcond = sync.NewCond(&s.qmu)
	for i := range s.links {
		s.links[i].seen = map[linkKey]struct{}{}
	}
	go s.run()
	return s
}

// Progress returns live counters.
func (s *Scanner) Progress() Progress {
	ms := s.elapsed.Load()
	if !s.done.Load() {
		ms = nowMillis() - s.started
	}
	return Progress{Files: s.files.Load(), Dirs: s.dirs.Load(), Bytes: s.bytes.Load(), Done: s.done.Load(), Millis: ms}
}

// Wait blocks until the scan ends and returns the tree.
func (s *Scanner) Wait() *Tree {
	t := <-s.result
	s.result <- t
	return t
}

func (s *Scanner) run() {
	top := &node{}
	for _, r := range dedupeRoots(s.opts.Roots, s.mnt, s.opts.Cross) {
		var st syscall.Stat_t
		n := &node{name: r, parent: top}
		top.dirs = append(top.dirs, n)
		if err := syscall.Lstat(r, &st); err != nil || st.Mode&syscall.S_IFMT != syscall.S_IFDIR {
			n.err = true
			continue
		}
		n.size = st.Blocks * 512
		s.push(job{n, r, st.Dev})
	}
	var wg sync.WaitGroup
	for i := 0; i < s.opts.Workers; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); s.worker() }()
	}
	wg.Wait()
	aggregate(top)
	t := &Tree{top: top, opts: s.opts}
	t.Top = t.entry(top, -1)
	s.elapsed.Store(nowMillis() - s.started)
	s.done.Store(true)
	s.result <- t
}

func (s *Scanner) push(j job) {
	s.qmu.Lock()
	s.queue = append(s.queue, j)
	s.pending++
	s.qmu.Unlock()
	s.qcond.Signal()
}

func (s *Scanner) worker() {
	buf := make([]byte, 64<<10)
	for {
		s.qmu.Lock()
		for len(s.queue) == 0 && s.pending > 0 {
			s.qcond.Wait()
		}
		if s.pending == 0 {
			s.qmu.Unlock()
			s.qcond.Broadcast()
			return
		}
		j := s.queue[len(s.queue)-1] // LIFO keeps the queue short (depth-first)
		s.queue = s.queue[:len(s.queue)-1]
		s.qmu.Unlock()

		s.readDir(j, buf)

		s.qmu.Lock()
		s.pending--
		last := s.pending == 0
		s.qmu.Unlock()
		if last {
			s.qcond.Broadcast()
		}
	}
}

// readDir lists one directory with raw getdents and fstatat, so no per-entry
// path strings or os.FileInfo values are allocated.
func (s *Scanner) readDir(j job, buf []byte) {
	n := j.n
	s.dirs.Add(1)
	s.bytes.Add(n.size)
	fd, err := syscall.Open(j.path, syscall.O_RDONLY|syscall.O_DIRECTORY|syscall.O_CLOEXEC|syscall.O_NOFOLLOW, 0)
	if err != nil {
		n.err = true
		return
	}
	defer syscall.Close(fd)
	var st syscall.Stat_t
	for {
		nb, err := syscall.Getdents(fd, buf)
		if err != nil {
			n.err = true
			return
		}
		if nb <= 0 {
			return
		}
		for off := 0; off < nb; {
			reclen := int(*(*uint16)(unsafe.Pointer(&buf[off+16])))
			name := direntName(buf[off+19 : off+reclen])
			off += reclen
			if name == "." || name == ".." {
				continue
			}
			if fstatat(fd, name, &st) != nil {
				continue // vanished or unreadable entry
			}
			blocks := st.Blocks * 512
			if st.Mode&syscall.S_IFMT == syscall.S_IFDIR {
				child := &node{name: name, parent: n, size: blocks}
				n.dirs = append(n.dirs, child)
				cp := joinPath(j.path, name)
				if (st.Dev != j.dev && !s.opts.Cross) || s.mnt.skip(cp, s.opts.Cross) {
					child.mount, child.size = true, 0
					continue
				}
				s.push(job{child, cp, st.Dev})
				continue
			}
			if st.Nlink > 1 && s.seenLink(st.Dev, st.Ino) {
				blocks = 0
			}
			s.files.Add(1)
			s.bytes.Add(blocks)
			n.files = append(n.files, file{off: uint32(len(n.names)), n: uint32(len(name)), size: blocks})
			n.names = append(n.names, name...)
		}
	}
}

func (s *Scanner) seenLink(dev, ino uint64) bool {
	sh := &s.links[ino%uint64(len(s.links))]
	k := linkKey{dev, ino}
	sh.Lock()
	defer sh.Unlock()
	if _, ok := sh.seen[k]; ok {
		return true
	}
	sh.seen[k] = struct{}{}
	return false
}

func direntName(b []byte) string {
	for i, c := range b {
		if c == 0 {
			return string(b[:i])
		}
	}
	return string(b)
}

func joinPath(dir, name string) string {
	if strings.HasSuffix(dir, "/") {
		return dir + name
	}
	return dir + "/" + name
}

// fstatat is lstat relative to an open directory.
// The stdlib syscall package has no exported fstatat on linux/amd64, so the
// raw newfstatat syscall is used (golang.org/x/sys would be a dependency).
func fstatat(dirfd int, name string, st *syscall.Stat_t) error {
	p, err := syscall.BytePtrFromString(name)
	if err != nil {
		return err
	}
	_, _, e := syscall.Syscall6(syscall.SYS_NEWFSTATAT, uintptr(dirfd), uintptr(unsafe.Pointer(p)),
		uintptr(unsafe.Pointer(st)), atSymlinkNoFollow, 0, 0)
	if e != 0 {
		return e
	}
	return nil
}

const atSymlinkNoFollow = 0x100

// aggregate turns per-directory own sizes into subtree totals.
func aggregate(n *node) {
	n.count = 1 + int64(len(n.files))
	for _, f := range n.files {
		n.size += f.size
	}
	for _, d := range n.dirs {
		aggregate(d)
		n.size += d.size
		n.count += d.count
	}
}

// dedupeRoots drops a root that its ancestor root's walk reaches anyway, so
// the default "/ and /home" scans /home once when it shares /'s filesystem.
func dedupeRoots(roots []string, m mounts, cross bool) []string {
	clean := make([]string, 0, len(roots))
	seen := map[string]bool{}
	for _, r := range roots {
		if abs, err := filepath.Abs(r); err == nil {
			r = abs
		}
		if !seen[r] {
			seen[r] = true
			clean = append(clean, r)
		}
	}
	sort.Strings(clean)
	var out []string
	for _, r := range clean {
		covered := false
		for _, p := range out {
			if under(r, p) && reachable(p, r, m, cross) {
				covered = true
				break
			}
		}
		if !covered {
			out = append(out, r)
		}
	}
	return out
}

func under(path, dir string) bool {
	return dir == "/" || strings.HasPrefix(path, dir+"/")
}

// reachable reports whether a walk of p enters r: no skipped mountpoint and
// no device change lies on the way down.
func reachable(p, r string, m mounts, cross bool) bool {
	var pst syscall.Stat_t
	if syscall.Stat(p, &pst) != nil {
		return false
	}
	cur := p
	for _, c := range strings.Split(strings.TrimPrefix(strings.TrimPrefix(r, p), "/"), "/") {
		cur = joinPath(cur, c)
		var st syscall.Stat_t
		if m.skip(cur, cross) || syscall.Lstat(cur, &st) != nil || (!cross && st.Dev != pst.Dev) {
			return false
		}
	}
	return true
}
