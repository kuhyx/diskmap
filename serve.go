package main

import (
	"crypto/rand"
	"embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/kuhyx/diskmap/scan"
	"github.com/kuhyx/diskmap/server"
)

//go:embed web
var webFS embed.FS

func webRoot() fs.FS {
	sub, err := fs.Sub(webFS, "web")
	if err != nil {
		panic(err)
	}
	return sub
}

func newToken() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// listen binds 127.0.0.1 only, so the UI is never reachable from the LAN.
func listen(port int) (net.Listener, string, error) {
	ln, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", port))
	if err != nil {
		return nil, "", err
	}
	return ln, "http://" + ln.Addr().String() + "/", nil
}

func serve(opts scan.Options, port int, browser bool) error {
	ln, url, err := listen(port)
	if err != nil {
		return err
	}
	srv := server.New(opts, newToken(), webRoot())
	fmt.Println(url)
	if browser {
		if err := exec.Command("xdg-open", url).Start(); err != nil {
			fmt.Fprintln(os.Stderr, "open browser:", err)
		}
	}
	return http.Serve(ln, srv.Handler())
}

// selfTest scans a temp fixture through the real HTTP stack and trashes a
// file into a temp XDG trash: the deploy health check.
func selfTest() error {
	dir, err := os.MkdirTemp("", "diskmap-selftest-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(dir)
	if err := os.Setenv("XDG_DATA_HOME", filepath.Join(dir, "data")); err != nil {
		return err
	}
	root := filepath.Join(dir, "root")
	victim := filepath.Join(root, "sub", "victim.bin")
	if err := os.MkdirAll(filepath.Dir(victim), 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(victim, make([]byte, 64<<10), 0o644); err != nil {
		return err
	}
	ln, url, err := listen(0)
	if err != nil {
		return err
	}
	tok := newToken()
	srv := server.New(scan.Options{Roots: []string{root}}, tok, webRoot())
	go func() { _ = http.Serve(ln, srv.Handler()) }()
	defer ln.Close()
	srv.Wait()

	call := func(method, path, body string) ([]byte, error) {
		req, err := http.NewRequest(method, url+strings.TrimPrefix(path, "/"), strings.NewReader(body))
		if err != nil {
			return nil, err
		}
		req.Header.Set("X-Token", tok)
		c := http.Client{Timeout: 5 * time.Second}
		res, err := c.Do(req)
		if err != nil {
			return nil, err
		}
		defer res.Body.Close()
		b, _ := io.ReadAll(res.Body)
		if res.StatusCode != 200 {
			return nil, fmt.Errorf("%s %s: %d", method, path, res.StatusCode)
		}
		return b, nil
	}
	page, err := call("GET", "/", "")
	if err != nil || !strings.Contains(string(page), tok) {
		return fmt.Errorf("index page: %v", err)
	}
	if _, err := call("GET", "/app.js", ""); err != nil {
		return err
	}
	b, err := call("GET", "/api/tree?path="+root, "")
	if err != nil {
		return err
	}
	var n server.Node
	if err := json.Unmarshal(b, &n); err != nil || n.Size < 64<<10 {
		return fmt.Errorf("tree size %d: %v", n.Size, err)
	}
	b, err = call("POST", "/api/delete", fmt.Sprintf(`{"paths":[%q]}`, victim))
	if err != nil {
		return err
	}
	var res []server.Result
	if err := json.Unmarshal(b, &res); err != nil || len(res) != 1 || !res[0].OK {
		return fmt.Errorf("delete: %s", b)
	}
	if _, err := os.Stat(filepath.Join(dir, "data", "Trash", "files", "victim.bin")); err != nil {
		return errors.New("victim not in trash")
	}
	return nil
}
