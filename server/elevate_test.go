package server

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kuhyx/diskmap/scan"
)

type eresult struct {
	Path      string `json:"path"`
	OK        bool   `json:"ok"`
	Error     string `json:"error"`
	NeedsRoot bool   `json:"needs_root"`
	Owned     bool   `json:"owned"`
	Cancelled bool   `json:"cancelled"`
}

func post(t *testing.T, s *Server, body map[string]any) []eresult {
	t.Helper()
	b, _ := json.Marshal(body)
	rec := do(s, "POST", "/api/delete", string(b), map[string]string{"X-Token": tok, "Origin": "http://127.0.0.1:8000"})
	if rec.Code != 200 {
		t.Fatalf("delete: %d %s", rec.Code, rec.Body.String())
	}
	var out []eresult
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	return out
}

// fakePkexec writes a pkexec stand-in that logs its argv to log and then
// exits with code; code 0 makes the parent writable and runs the command.
func fakePkexec(t *testing.T, code int) (bin, log string) {
	t.Helper()
	dir := t.TempDir()
	bin, log = filepath.Join(dir, "pkexec"), filepath.Join(dir, "argv")
	script := "#!/bin/sh\nprintf '%s\\n' \"$@\" > " + log + "\n"
	switch code {
	case 0:
		script += "for a; do last=$a; done\nchmod -R u+w \"$(dirname \"$last\")\"\nexec \"$@\"\n"
	case 127:
		script += "echo 'Error executing command as another user: No authentication agent found.' >&2\nexit 127\n"
	default:
		script += "exit 126\n"
	}
	if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return bin, log
}

// lockedServer scans a tree with a read-only dir "ro" (a fake root-owned
// folder: the user cannot remove what is inside it).
func lockedServer(t *testing.T) (*Server, string) {
	t.Helper()
	if os.Geteuid() == 0 {
		t.Skip("root ignores directory permissions")
	}
	s, root := newServer(t)
	ro := filepath.Join(root, "ro")
	if err := os.MkdirAll(filepath.Join(ro, "victim"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(ro, "victim", "f"), make([]byte, 20000), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, d := range []string{filepath.Join(ro, "victim"), ro} {
		if err := os.Chmod(d, 0o555); err != nil {
			t.Fatal(err)
		}
	}
	t.Cleanup(func() { _ = os.Chmod(ro, 0o755); _ = os.Chmod(filepath.Join(ro, "victim"), 0o755) })
	s2 := New(scan.Options{Roots: []string{root}}, tok, s.web)
	s2.Wait()
	s2.owner = func(string) bool { return false }
	return s2, root
}

func TestPermissionDeniedNeedsRoot(t *testing.T) {
	s, root := lockedServer(t)
	victim := filepath.Join(root, "ro", "victim")
	for _, perm := range []bool{false, true} {
		res := post(t, s, map[string]any{"paths": []string{victim}, "permanent": perm})
		if len(res) != 1 || res[0].OK || !res[0].NeedsRoot || res[0].Owned {
			t.Fatalf("permanent=%v: %+v", perm, res)
		}
	}
	if _, err := os.Stat(filepath.Join(victim, "f")); err != nil {
		t.Fatal("victim touched without elevation")
	}
}

func TestOwnedFlag(t *testing.T) {
	s, root := lockedServer(t)
	victim := filepath.Join(root, "ro", "victim")
	var asked string
	s.owner = func(p string) bool { asked = p; return true }
	res := post(t, s, map[string]any{"paths": []string{victim}})
	if !res[0].NeedsRoot || !res[0].Owned || asked != victim {
		t.Fatalf("%+v asked=%q", res, asked)
	}
}

func TestElevatedDeleteRunsPkexec(t *testing.T) {
	s, root := lockedServer(t)
	bin, log := fakePkexec(t, 0)
	s.elevate = pkexecElevator(bin)
	victim := filepath.Join(root, "ro", "victim")
	before := s.tree().Lookup(root).Size
	res := post(t, s, map[string]any{"paths": []string{victim}, "permanent": true, "elevate": true})
	if len(res) != 1 || !res[0].OK {
		t.Fatalf("%+v", res)
	}
	argv, _ := os.ReadFile(log)
	want := "/usr/bin/rm\n-rf\n--one-file-system\n--\n" + victim + "\n"
	if string(argv) != want {
		t.Fatalf("pkexec argv %q, want %q", argv, want)
	}
	if _, err := os.Lstat(victim); !os.IsNotExist(err) {
		t.Fatal("victim still there")
	}
	if s.tree().Lookup(victim) != nil || s.tree().Lookup(root).Size >= before {
		t.Fatal("tree not updated")
	}
}

func TestElevateNeedsPermanent(t *testing.T) {
	s, root := lockedServer(t)
	called := false
	s.elevate = func(string) error { called = true; return nil }
	res := post(t, s, map[string]any{"paths": []string{filepath.Join(root, "ro", "victim")}, "elevate": true})
	if res[0].OK || called {
		t.Fatalf("elevated trash allowed: %+v", res)
	}
}

func TestElevatedCancelAndNoAgent(t *testing.T) {
	s, root := lockedServer(t)
	victim := filepath.Join(root, "ro", "victim")
	bin, _ := fakePkexec(t, 126)
	s.elevate = pkexecElevator(bin)
	res := post(t, s, map[string]any{"paths": []string{victim}, "permanent": true, "elevate": true})
	if res[0].OK || !res[0].Cancelled || res[0].Error != "" {
		t.Fatalf("cancel: %+v", res)
	}
	bin, _ = fakePkexec(t, 127)
	s.elevate = pkexecElevator(bin)
	res = post(t, s, map[string]any{"paths": []string{victim}, "permanent": true, "elevate": true})
	if res[0].OK || res[0].Cancelled || !strings.Contains(res[0].Error, "No authentication agent") {
		t.Fatalf("no agent: %+v", res)
	}
	s.elevate = pkexecElevator(filepath.Join(t.TempDir(), "missing-pkexec"))
	res = post(t, s, map[string]any{"paths": []string{victim}, "permanent": true, "elevate": true})
	if res[0].OK || res[0].Error == "" {
		t.Fatalf("missing pkexec: %+v", res)
	}
	if _, err := os.Stat(filepath.Join(victim, "f")); err != nil {
		t.Fatal("victim removed")
	}
}

func TestElevatedSymlinkDeletesLinkOnly(t *testing.T) {
	s, root := newServer(t)
	if os.Geteuid() == 0 {
		t.Skip("root")
	}
	target := filepath.Join(root, "b")
	link := filepath.Join(root, "ln")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	s2 := New(scan.Options{Roots: []string{root}}, tok, s.web)
	s2.Wait()
	bin, log := fakePkexec(t, 0)
	s2.elevate = pkexecElevator(bin)
	res := post(t, s2, map[string]any{"paths": []string{link}, "permanent": true, "elevate": true})
	if !res[0].OK {
		t.Fatalf("%+v", res)
	}
	argv, _ := os.ReadFile(log)
	if !strings.HasSuffix(string(argv), "\n"+link+"\n") {
		t.Fatalf("argv %q", argv)
	}
	if _, err := os.Lstat(link); !os.IsNotExist(err) {
		t.Fatal("link still there")
	}
	if _, err := os.Stat(filepath.Join(target, "g")); err != nil {
		t.Fatal("symlink target damaged")
	}
}

func TestProtectedPaths(t *testing.T) {
	home, _ := os.UserHomeDir()
	for _, p := range []string{"/", "/usr", "/etc", "/home", "/tmp", "/root", "/sys", home, "/usr/", "/usr/../usr"} {
		if !isProtected(filepath.Clean(p)) {
			t.Errorf("%s not protected", p)
		}
	}
	for _, p := range []string{"/usr/share/dm-test", "/tmp/x", "/etc/foo", filepath.Join(home, "x")} {
		if isProtected(p) {
			t.Errorf("%s protected", p)
		}
	}
}

// The protected list is re-checked on the path after its parent is resolved.
func TestResolveRechecksProtected(t *testing.T) {
	dir := t.TempDir()
	if err := os.Symlink("/", filepath.Join(dir, "slash")); err != nil {
		t.Fatal(err)
	}
	if _, err := resolveElevated(filepath.Join(dir, "slash", "usr")); err == nil {
		t.Fatal("resolved /usr not refused")
	}
	got, err := resolveElevated(filepath.Join(dir, "slash"))
	if err != nil || got != filepath.Join(dir, "slash") {
		t.Fatalf("link itself: %q %v", got, err)
	}
}

func TestProtectedRefusedForTrash(t *testing.T) {
	s, _ := newServer(t)
	home, _ := os.UserHomeDir()
	res := post(t, s, map[string]any{"paths": []string{"/tmp", home}})
	for _, r := range res {
		if r.OK || r.Error == "" {
			t.Fatalf("%+v", r)
		}
	}
}
