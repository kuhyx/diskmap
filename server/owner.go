package server

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// maxOwnerArgs caps the children passed to one pacman call (ARG_MAX safety);
// the flag is a hint, so a sample of a huge dir is enough.
const maxOwnerArgs = 2000

// pacmanOwned reports whether pacman owns p or, for a directory, any of its
// direct children. False when pacman is missing. -q prints only package
// names, so the output is locale-proof; the exit status is non-zero whenever
// one argument is unowned, so it is ignored.
func pacmanOwned(p string) bool {
	bin, err := exec.LookPath("pacman")
	if err != nil {
		return false
	}
	args := []string{"-Qqo", "--", p}
	if ents, err := os.ReadDir(p); err == nil {
		for _, e := range ents {
			if len(args) >= maxOwnerArgs {
				break
			}
			args = append(args, filepath.Join(p, e.Name()))
		}
	}
	cmd := exec.Command(bin, args...)
	cmd.Env = append(os.Environ(), "LC_ALL=C")
	out, _ := cmd.Output()
	return strings.TrimSpace(string(out)) != ""
}
