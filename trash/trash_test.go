package trash

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func setup(t *testing.T) (home, work string) {
	t.Helper()
	base := t.TempDir()
	home = filepath.Join(base, "data")
	work = filepath.Join(base, "work")
	if err := os.MkdirAll(work, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("XDG_DATA_HOME", home)
	return home, work
}

func TestTrashMovesFileAndWritesInfo(t *testing.T) {
	home, work := setup(t)
	f := filepath.Join(work, "a b%.txt")
	if err := os.WriteFile(f, []byte("hi"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := Trash(f); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(f); !os.IsNotExist(err) {
		t.Fatal("source still exists")
	}
	if b, err := os.ReadFile(filepath.Join(home, "Trash", "files", "a b%.txt")); err != nil || string(b) != "hi" {
		t.Fatalf("trashed file: %q %v", b, err)
	}
	info, err := os.ReadFile(filepath.Join(home, "Trash", "info", "a b%.txt.trashinfo"))
	if err != nil {
		t.Fatal(err)
	}
	s := string(info)
	if !strings.HasPrefix(s, "[Trash Info]\nPath=") || !strings.Contains(s, "a%20b%25.txt") || !strings.Contains(s, "DeletionDate=") {
		t.Fatalf("bad trashinfo:\n%s", s)
	}
}

func TestTrashNameCollision(t *testing.T) {
	home, work := setup(t)
	for i := 0; i < 2; i++ {
		d := filepath.Join(work, "dir")
		if err := os.MkdirAll(filepath.Join(d, "sub"), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := Trash(d); err != nil {
			t.Fatal(err)
		}
	}
	for _, n := range []string{"dir", "dir.2"} {
		if _, err := os.Stat(filepath.Join(home, "Trash", "files", n, "sub")); err != nil {
			t.Fatalf("%s: %v", n, err)
		}
	}
}

func TestTrashDoesNotFollowSymlink(t *testing.T) {
	home, work := setup(t)
	target := filepath.Join(work, "target")
	if err := os.WriteFile(target, []byte("keep"), 0o644); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(work, "link")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	if err := Trash(link); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(target); err != nil {
		t.Fatal("symlink target was moved")
	}
	if fi, err := os.Lstat(filepath.Join(home, "Trash", "files", "link")); err != nil || fi.Mode()&os.ModeSymlink == 0 {
		t.Fatal("link not trashed as a link")
	}
}

func TestTrashErrorLeavesNoInfo(t *testing.T) {
	home, work := setup(t)
	locked := filepath.Join(work, "locked")
	if err := os.MkdirAll(locked, 0o755); err != nil {
		t.Fatal(err)
	}
	f := filepath.Join(locked, "f")
	if err := os.WriteFile(f, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(locked, 0o555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(locked, 0o755) })
	if err := Trash(f); err == nil {
		t.Fatal("expected error for file in read-only dir")
	}
	if ents, _ := os.ReadDir(filepath.Join(home, "Trash", "info")); len(ents) != 0 {
		t.Fatalf("stale trashinfo left: %v", ents)
	}
}

func TestDeleteRemovesTree(t *testing.T) {
	_, work := setup(t)
	d := filepath.Join(work, "d")
	if err := os.MkdirAll(filepath.Join(d, "x", "y"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := Delete(d); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(d); !os.IsNotExist(err) {
		t.Fatal("still exists")
	}
}
