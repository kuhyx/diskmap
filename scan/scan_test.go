package scan

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// duBytes returns `du -x -s -B1 path`, the reference the scanner must match.
func duBytes(t *testing.T, path string) int64 {
	t.Helper()
	out, err := exec.Command("du", "-x", "-s", "-B1", path).Output()
	if err != nil && len(out) == 0 {
		t.Fatalf("du: %v", err)
	}
	n, err := strconv.ParseInt(strings.Fields(string(out))[0], 10, 64)
	if err != nil {
		t.Fatal(err)
	}
	return n
}

func write(t *testing.T, path string, n int) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(strings.Repeat("x", n)), 0o644); err != nil {
		t.Fatal(err)
	}
}

// fixture builds nested dirs, a hardlink, a symlink and an unreadable dir.
func fixture(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	write(t, filepath.Join(root, "a", "big.bin"), 300_000)
	write(t, filepath.Join(root, "a", "b", "mid.bin"), 70_000)
	write(t, filepath.Join(root, "a", "b", "c", "small.bin"), 10)
	write(t, filepath.Join(root, "z.bin"), 5_000)
	if err := os.Link(filepath.Join(root, "a", "big.bin"), filepath.Join(root, "hard.bin")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(root, "a"), filepath.Join(root, "link")); err != nil {
		t.Fatal(err)
	}
	locked := filepath.Join(root, "locked")
	write(t, filepath.Join(locked, "secret.bin"), 50_000)
	if err := os.Chmod(locked, 0); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(locked, 0o755) })
	return root
}

func TestScanMatchesDu(t *testing.T) {
	root := fixture(t)
	tree := Scan(Options{Roots: []string{root}})
	got := tree.Lookup(root)
	if got == nil {
		t.Fatalf("root %s not in tree", root)
	}
	if want := duBytes(t, root); got.Size != want {
		t.Fatalf("size %d, du says %d", got.Size, want)
	}
	if !tree.Lookup(filepath.Join(root, "locked")).Err {
		t.Fatal("unreadable dir not flagged")
	}
	sub := tree.Lookup(filepath.Join(root, "a", "b"))
	if want := duBytes(t, filepath.Join(root, "a", "b")); sub.Size != want {
		t.Fatalf("a/b size %d, du says %d", sub.Size, want)
	}
}

func TestChildrenSortedBiggestFirst(t *testing.T) {
	root := fixture(t)
	tree := Scan(Options{Roots: []string{root}})
	kids := tree.Children(tree.Lookup(root))
	if len(kids) == 0 {
		t.Fatal("no children")
	}
	for i := 1; i < len(kids); i++ {
		if kids[i].Size > kids[i-1].Size {
			t.Fatalf("not sorted: %v", kids)
		}
	}
	names := map[string]bool{}
	for _, k := range kids {
		names[k.Name] = true
	}
	for _, want := range []string{"a", "z.bin", "hard.bin", "link", "locked"} {
		if !names[want] {
			t.Fatalf("missing child %q in %v", want, names)
		}
	}
}

func TestNestedRootDeduped(t *testing.T) {
	root := fixture(t)
	tree := Scan(Options{Roots: []string{root, filepath.Join(root, "a")}})
	if n := len(tree.Children(tree.Top)); n != 1 {
		t.Fatalf("want 1 top-level root, got %d", n)
	}
}

func TestRemove(t *testing.T) {
	root := fixture(t)
	tree := Scan(Options{Roots: []string{root}})
	before := tree.Lookup(root).Size
	b := tree.Lookup(filepath.Join(root, "a", "b"))
	bs := b.Size
	tree.Remove(b)
	if tree.Lookup(filepath.Join(root, "a", "b")) != nil {
		t.Fatal("node still present")
	}
	if got := tree.Lookup(root).Size; got != before-bs {
		t.Fatalf("root size %d, want %d", got, before-bs)
	}
}

func TestSkipsMountsUnlessCross(t *testing.T) {
	m := parseMountinfo(strings.NewReader(
		"22 1 0:21 / /proc rw - proc proc rw\n" +
			"1 0 259:2 / / rw - ext4 /dev/x rw\n" +
			"30 1 259:2 /etc/hosts /etc/hosts rw - ext4 /dev/x rw\n" +
			"31 1 8:1 / /mnt/data\\040disk rw - ext4 /dev/y rw\n"))
	if !m.skip("/proc", false) || !m.skip("/proc", true) {
		t.Fatal("proc must always be skipped")
	}
	if !m.skip("/mnt/data disk", false) || m.skip("/mnt/data disk", true) {
		t.Fatal("real mount skipped only without --cross")
	}
	if m.skip("/", false) || m.skip("/usr", false) {
		t.Fatal("non-mount skipped")
	}
}
