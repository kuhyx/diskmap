package scan

import (
	"path/filepath"
	"sort"
	"strings"
	"syscall"
	"time"
)

func nowMillis() int64 { return time.Now().UnixMilli() }

func (n *node) path() string {
	if n.parent == nil {
		return ""
	}
	if n.parent.parent == nil {
		return n.name // a root keeps its absolute path as name
	}
	return joinPath(n.parent.path(), n.name)
}

func (t *Tree) entry(n *node, fi int) *Entry {
	if fi >= 0 {
		f := n.files[fi]
		name := n.fileName(f)
		return &Entry{Name: name, Path: joinPath(n.path(), name), Size: f.size, Count: 1, dir: n, file: fi}
	}
	return &Entry{Name: n.name, Path: n.path(), Size: n.size, Count: n.count, IsDir: true, Err: n.err, Mount: n.mount, dir: n, file: -1}
}

// Roots returns the absolute paths that were actually scanned.
func (t *Tree) Roots() []string {
	t.mu.RLock()
	defer t.mu.RUnlock()
	out := make([]string, len(t.top.dirs))
	for i, d := range t.top.dirs {
		out[i] = d.name
	}
	return out
}

// Root returns the scanned root that contains path, or "".
func (t *Tree) Root(path string) string {
	t.mu.RLock()
	defer t.mu.RUnlock()
	if r := t.rootFor(filepath.Clean(path)); r != nil {
		return r.name
	}
	return ""
}

func (t *Tree) rootFor(path string) *node {
	var best *node
	for _, r := range t.top.dirs {
		if (path == r.name || under(path, r.name)) && (best == nil || len(r.name) > len(best.name)) {
			best = r
		}
	}
	return best
}

// Lookup finds the entry at an absolute path; "" is the virtual top.
func (t *Tree) Lookup(path string) *Entry {
	t.mu.RLock()
	defer t.mu.RUnlock()
	n, fi := t.find(path)
	if n == nil {
		return nil
	}
	return t.entry(n, fi)
}

func (t *Tree) find(path string) (*node, int) {
	if path == "" {
		return t.top, -1
	}
	path = filepath.Clean(path)
	n := t.rootFor(path)
	if n == nil {
		return nil, -1
	}
	rest := strings.TrimPrefix(strings.TrimPrefix(path, n.name), "/")
	if rest == "" {
		return n, -1
	}
	parts := strings.Split(rest, "/")
	for i, p := range parts {
		var next *node
		for _, d := range n.dirs {
			if d.name == p {
				next = d
				break
			}
		}
		if next != nil {
			n = next
			continue
		}
		if i == len(parts)-1 {
			for fi, f := range n.files {
				if n.fileName(f) == p {
					return n, fi
				}
			}
		}
		return nil, -1
	}
	return n, -1
}

// Children lists a directory's dirs and files, biggest first.
func (t *Tree) Children(e *Entry) []*Entry {
	if e == nil || !e.IsDir {
		return nil
	}
	t.mu.RLock()
	defer t.mu.RUnlock()
	n := e.dir
	out := make([]*Entry, 0, len(n.dirs)+len(n.files))
	for _, d := range n.dirs {
		out = append(out, t.entry(d, -1))
	}
	for i := range n.files {
		out = append(out, t.entry(n, i))
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Size != out[j].Size {
			return out[i].Size > out[j].Size
		}
		return out[i].Name < out[j].Name
	})
	return out
}

// Remove drops an entry and subtracts its size from every ancestor.
func (t *Tree) Remove(e *Entry) {
	t.mu.Lock()
	defer t.mu.Unlock()
	n, fi := t.find(e.Path)
	if n == nil {
		return
	}
	t.removeLocked(n, fi)
}

func (t *Tree) removeLocked(n *node, fi int) {
	var size, count int64
	var parent *node
	if fi >= 0 {
		size, count, parent = n.files[fi].size, 1, n
		n.files = append(n.files[:fi], n.files[fi+1:]...)
	} else {
		size, count, parent = n.size, n.count, n.parent
		if parent == nil {
			return
		}
		for i, d := range parent.dirs {
			if d == n {
				parent.dirs = append(parent.dirs[:i], parent.dirs[i+1:]...)
				break
			}
		}
	}
	for p := parent; p != nil; p = p.parent {
		p.size -= size
		p.count -= count
	}
}

// Refresh re-reads path from disk after a delete attempt: a gone path is
// removed, a partly removed directory is rescanned and swapped in.
func (t *Tree) Refresh(path string) {
	path = filepath.Clean(path)
	var st syscall.Stat_t
	gone := syscall.Lstat(path, &st) != nil
	var fresh *node
	if !gone && st.Mode&syscall.S_IFMT == syscall.S_IFDIR {
		opts := t.opts
		opts.Roots = []string{path}
		sub := Scan(opts)
		if len(sub.top.dirs) == 1 {
			fresh = sub.top.dirs[0]
		}
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	n, fi := t.find(path)
	if n == nil {
		return
	}
	if gone {
		t.removeLocked(n, fi)
		return
	}
	if fi >= 0 {
		delta := st.Blocks*512 - n.files[fi].size
		n.files[fi].size += delta
		for p := n; p != nil; p = p.parent {
			p.size += delta
		}
		return
	}
	if fresh == nil || n.parent == nil {
		return
	}
	dSize, dCount := fresh.size-n.size, fresh.count-n.count
	n.dirs, n.files, n.names, n.size, n.count, n.err = fresh.dirs, fresh.files, fresh.names, fresh.size, fresh.count, fresh.err
	for _, d := range n.dirs {
		d.parent = n
	}
	for p := n.parent; p != nil; p = p.parent {
		p.size += dSize
		p.count += dCount
	}
}
