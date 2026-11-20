package server

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// protectedDirs are refused as delete/trash targets themselves; anything
// below them is allowed. $HOME is added at lookup time.
var protectedDirs = []string{
	"/", "/usr", "/etc", "/boot", "/bin", "/sbin", "/lib", "/lib64", "/var",
	"/home", "/root", "/opt", "/srv", "/dev", "/proc", "/sys", "/run", "/tmp",
}

// isProtected reports whether the clean absolute path p is a protected dir
// or the user's home (raw or resolved).
func isProtected(p string) bool {
	for _, d := range protectedDirs {
		if p == d {
			return true
		}
	}
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		if p == filepath.Clean(home) {
			return true
		}
		if real, err := filepath.EvalSymlinks(home); err == nil && p == real {
			return true
		}
	}
	return false
}

var errProtected = errors.New("protected path")

// resolveElevated resolves the parent of p (never the final element, so a
// symlink is deleted as a link) and re-checks the protected list on the
// result: what runs as root only ever sees the resolved path.
func resolveElevated(p string) (string, error) {
	parent, err := filepath.EvalSymlinks(filepath.Dir(p))
	if err != nil {
		return "", err
	}
	real := filepath.Join(parent, filepath.Base(p))
	if !filepath.IsAbs(real) || isProtected(real) {
		return "", fmt.Errorf("%w: %s", errProtected, real)
	}
	return real, nil
}
