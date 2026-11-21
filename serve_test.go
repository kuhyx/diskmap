package main

import (
	"io/fs"
	"strings"
	"testing"
)

func TestWebFSContainsCreditFiles(t *testing.T) {
	wfs := webRoot()

	// index.html must contain the kuhy-credit tag and vendor JS reference.
	idx, err := fs.ReadFile(wfs, "index.html")
	if err != nil {
		t.Fatalf("read index.html: %v", err)
	}
	got := string(idx)
	if !strings.Contains(got, "<kuhy-credit") {
		t.Error("index.html missing <kuhy-credit>")
	}
	if !strings.Contains(got, "vendor/kuhy-credit/kuhy-credit.js") {
		t.Error("index.html missing vendor/kuhy-credit/kuhy-credit.js reference")
	}

	// Vendor files must exist.
	for _, p := range []string{"vendor/kuhy-credit/kuhy-credit.js", "vendor/kuhy-credit/credit-data.js"} {
		fi, err := fs.Stat(wfs, p)
		if err != nil {
			t.Errorf("%s: %v", p, err)
			continue
		}
		if fi.IsDir() {
			t.Errorf("%s: expected file, got dir", p)
		}
	}
}
