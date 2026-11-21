package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/kuhyx/diskmap/scan"
)

const tok = "secret-token"

func newServer(t *testing.T) (*Server, string) {
	t.Helper()
	root := t.TempDir()
	t.Setenv("XDG_DATA_HOME", filepath.Join(t.TempDir(), "data"))
	for _, p := range []string{"a/f1", "a/f2", "b/g", "keep"} {
		full := filepath.Join(root, p)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(strings.Repeat("x", 9000)), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	web := fstest.MapFS{
		"index.html":                        {Data: []byte("<html>{{TOKEN}}</html>")},
		"vendor/kuhy-credit/kuhy-credit.js": {Data: []byte("export {}")},
	}
	s := New(scan.Options{Roots: []string{root}}, tok, web)
	s.Wait()
	return s, root
}

func do(s *Server, method, url, body string, hdr map[string]string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, url, strings.NewReader(body))
	req.Host = "127.0.0.1:8000"
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, req)
	return rec
}

func TestIndexInjectsToken(t *testing.T) {
	s, _ := newServer(t)
	rec := do(s, "GET", "/", "", nil)
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), tok) {
		t.Fatalf("index: %d %q", rec.Code, rec.Body.String())
	}
}

func TestRejectsForeignHost(t *testing.T) {
	s, _ := newServer(t)
	req := httptest.NewRequest("GET", "/", nil)
	req.Host = "evil.example:8000" // DNS rebinding
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("want 403, got %d", rec.Code)
	}
}

func TestAPIRequiresToken(t *testing.T) {
	s, _ := newServer(t)
	if rec := do(s, "GET", "/api/tree", "", nil); rec.Code != http.StatusForbidden {
		t.Fatalf("tree without token: %d", rec.Code)
	}
	if rec := do(s, "GET", "/api/tree", "", map[string]string{"X-Token": tok}); rec.Code != 200 {
		t.Fatalf("tree with token: %d", rec.Code)
	}
}

func TestDeleteRejectsForeignOrigin(t *testing.T) {
	s, root := newServer(t)
	body := `{"paths":["` + filepath.Join(root, "keep") + `"]}`
	rec := do(s, "POST", "/api/delete", body, map[string]string{"X-Token": tok, "Origin": "http://evil.example"})
	if rec.Code != http.StatusForbidden {
		t.Fatalf("want 403, got %d", rec.Code)
	}
	if _, err := os.Stat(filepath.Join(root, "keep")); err != nil {
		t.Fatal("file removed despite foreign origin")
	}
}

type result struct {
	Path  string `json:"path"`
	OK    bool   `json:"ok"`
	Error string `json:"error"`
}

func deleteReq(t *testing.T, s *Server, paths []string, permanent bool) []result {
	t.Helper()
	b, _ := json.Marshal(map[string]any{"paths": paths, "permanent": permanent})
	rec := do(s, "POST", "/api/delete", string(b), map[string]string{"X-Token": tok, "Origin": "http://127.0.0.1:8000"})
	if rec.Code != 200 {
		t.Fatalf("delete: %d %s", rec.Code, rec.Body.String())
	}
	var out []result
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	return out
}

func TestDeleteGuardsAndPartialFailure(t *testing.T) {
	s, root := newServer(t)
	before := s.tree().Lookup(root).Size
	res := deleteReq(t, s, []string{
		root,                        // a scan root: refused
		"/etc/passwd",               // outside the roots: refused
		filepath.Join(root, "a"),    // trashed
		filepath.Join(root, "nope"), // not in the tree
	}, false)
	ok := map[string]bool{}
	for _, r := range res {
		ok[r.Path] = r.OK
	}
	if ok[root] || ok["/etc/passwd"] || ok[filepath.Join(root, "nope")] || !ok[filepath.Join(root, "a")] {
		t.Fatalf("unexpected results %+v", res)
	}
	if _, err := os.Stat(filepath.Join(root, "a")); !os.IsNotExist(err) {
		t.Fatal("a not removed")
	}
	if _, err := os.Stat(filepath.Join(os.Getenv("XDG_DATA_HOME"), "Trash", "files", "a", "f1")); err != nil {
		t.Fatal("a not in trash")
	}
	if s.tree().Lookup(filepath.Join(root, "a")) != nil || s.tree().Lookup(root).Size >= before {
		t.Fatal("tree not updated")
	}
}

func TestPermanentDelete(t *testing.T) {
	s, root := newServer(t)
	res := deleteReq(t, s, []string{filepath.Join(root, "b")}, true)
	if len(res) != 1 || !res[0].OK {
		t.Fatalf("%+v", res)
	}
	if _, err := os.Stat(filepath.Join(os.Getenv("XDG_DATA_HOME"), "Trash", "files", "b")); err == nil {
		t.Fatal("permanent delete went to trash")
	}
}

func TestTreeDepth(t *testing.T) {
	s, root := newServer(t)
	rec := do(s, "GET", "/api/tree?depth=2&path="+root, "", map[string]string{"X-Token": tok})
	var n Node
	if err := json.Unmarshal(rec.Body.Bytes(), &n); err != nil {
		t.Fatal(err)
	}
	if n.Path != root || len(n.Kids) != 3 || n.Kids[0].Name != "a" || len(n.Kids[0].Kids) != 2 {
		t.Fatalf("bad tree %+v", n)
	}
}

func TestVendorKuhyCreditServes(t *testing.T) {
	s, _ := newServer(t)
	rec := do(s, "GET", "/vendor/kuhy-credit/kuhy-credit.js", "", nil)
	if rec.Code != 200 {
		t.Fatalf("vendor kuhy-credit.js: %d", rec.Code)
	}
	ct := rec.Header().Get("Content-Type")
	if !strings.Contains(ct, "javascript") && !strings.Contains(ct, "module") {
		t.Fatalf("unexpected content-type: %s", ct)
	}
}
