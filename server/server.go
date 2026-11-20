// Package server exposes a finished or running scan over loopback HTTP.
//
// Safety model: the listener binds 127.0.0.1 only; every request must carry
// a loopback Host header (stops DNS rebinding); /api/* needs the per-run
// token in X-Token (a custom header also forces a CORS preflight, which is
// never answered); /api/delete also rejects a foreign Origin and any path
// that is a scan root, outside the roots, or not in the scanned tree.
package server

import (
	"encoding/json"
	"errors"
	"io/fs"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/kuhyx/diskmap/scan"
	"github.com/kuhyx/diskmap/trash"
)

// Server serves one scan.
type Server struct {
	scanner *scan.Scanner
	token   string
	web     fs.FS
}

// New starts scanning in the background and returns the server.
func New(opts scan.Options, token string, web fs.FS) *Server {
	return &Server{scanner: scan.Start(opts), token: token, web: web}
}

// Wait blocks until the scan has finished.
func (s *Server) Wait() { s.scanner.Wait() }

func (s *Server) tree() *scan.Tree {
	if !s.scanner.Progress().Done {
		return nil
	}
	return s.scanner.Wait()
}

// Handler returns the HTTP handler.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /{$}", s.index)
	mux.Handle("GET /", http.FileServerFS(s.web))
	mux.HandleFunc("GET /api/progress", s.api(s.progress))
	mux.HandleFunc("GET /api/tree", s.api(s.treeHandler))
	mux.HandleFunc("POST /api/delete", s.api(s.deleteHandler))
	return s.guard(mux)
}

func loopbackHost(hostport string) bool {
	h, _, err := net.SplitHostPort(hostport)
	if err != nil {
		h = hostport
	}
	return h == "127.0.0.1" || h == "localhost"
}

func (s *Server) guard(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !loopbackHost(r.Host) {
			http.Error(w, "forbidden host", http.StatusForbidden)
			return
		}
		if o := r.Header.Get("Origin"); o != "" && o != "http://"+r.Host {
			http.Error(w, "forbidden origin", http.StatusForbidden)
			return
		}
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("X-Frame-Options", "DENY")
		next.ServeHTTP(w, r)
	})
}

func (s *Server) api(h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Token") != s.token {
			http.Error(w, "bad token", http.StatusForbidden)
			return
		}
		h(w, r)
	}
}

func (s *Server) index(w http.ResponseWriter, _ *http.Request) {
	b, err := fs.ReadFile(s.web, "index.html")
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write([]byte(strings.ReplaceAll(string(b), "{{TOKEN}}", s.token)))
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}

func (s *Server) progress(w http.ResponseWriter, _ *http.Request) {
	p := s.scanner.Progress()
	out := map[string]any{"progress": p}
	if t := s.tree(); t != nil {
		out["roots"] = t.Roots()
	}
	writeJSON(w, out)
}

// Node is an entry with (part of) its subtree.
type Node struct {
	scan.Entry
	Kids     []*Node `json:"kids,omitempty"`
	More     int     `json:"more,omitempty"`     // children left out by the limit
	MoreSize int64   `json:"moreSize,omitempty"` // their total size
}

func intParam(r *http.Request, name string, def, lo, hi int) int {
	v, err := strconv.Atoi(r.URL.Query().Get(name))
	if err != nil {
		return def
	}
	return max(lo, min(hi, v))
}

func (s *Server) treeHandler(w http.ResponseWriter, r *http.Request) {
	t := s.tree()
	if t == nil {
		http.Error(w, "scan running", http.StatusServiceUnavailable)
		return
	}
	e := t.Lookup(r.URL.Query().Get("path"))
	if e == nil {
		http.NotFound(w, r)
		return
	}
	depth := intParam(r, "depth", 1, 0, 4)
	limit := intParam(r, "limit", 500, 1, 5000)
	writeJSON(w, build(t, e, depth, limit))
}

func build(t *scan.Tree, e *scan.Entry, depth, limit int) *Node {
	n := &Node{Entry: *e}
	if depth == 0 || !e.IsDir {
		return n
	}
	kids := t.Children(e)
	if len(kids) > limit {
		n.More = len(kids) - limit
		for _, k := range kids[limit:] {
			n.MoreSize += k.Size
		}
		kids = kids[:limit]
	}
	n.Kids = make([]*Node, 0, len(kids))
	for _, k := range kids {
		n.Kids = append(n.Kids, build(t, k, depth-1, limit))
	}
	return n
}

// errText drops the paths Go wraps around a syscall error ("rename a b:
// permission denied" -> "permission denied"); the UI already shows the path.
func errText(err error) string {
	var pe *fs.PathError
	var le *os.LinkError
	switch {
	case errors.As(err, &le):
		return le.Err.Error()
	case errors.As(err, &pe):
		return pe.Err.Error()
	}
	return err.Error()
}

// Result reports what happened to one path of a delete request.
type Result struct {
	Path  string `json:"path"`
	OK    bool   `json:"ok"`
	Error string `json:"error,omitempty"`
}

func (s *Server) deleteHandler(w http.ResponseWriter, r *http.Request) {
	t := s.tree()
	if t == nil {
		http.Error(w, "scan running", http.StatusServiceUnavailable)
		return
	}
	var req struct {
		Paths     []string `json:"paths"`
		Permanent bool     `json:"permanent"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4<<20)).Decode(&req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	out := make([]Result, 0, len(req.Paths))
	for _, p := range req.Paths {
		out = append(out, s.deleteOne(t, p, req.Permanent))
	}
	writeJSON(w, out)
}

// deleteOne handles one path; each path is independent, so one failure
// never stops the rest.
func (s *Server) deleteOne(t *scan.Tree, p string, permanent bool) Result {
	res := Result{Path: p}
	if !filepath.IsAbs(p) || filepath.Clean(p) != p {
		res.Error = "path must be absolute and clean"
		return res
	}
	root := t.Root(p)
	switch {
	case root == "":
		res.Error = "outside the scanned roots"
		return res
	case root == p:
		res.Error = "refusing to delete a scan root"
		return res
	case t.Lookup(p) == nil:
		res.Error = "not in the scanned tree"
		return res
	}
	var err error
	if permanent {
		err = trash.Delete(p)
	} else {
		err = trash.Trash(p)
	}
	t.Refresh(p) // reflects a full or partial removal either way
	if err != nil {
		res.Error = errText(err)
		return res
	}
	res.OK = true
	return res
}
