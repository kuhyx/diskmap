package server

import (
	"bytes"
	"errors"
	"os"
	"os/exec"
	"strings"
)

// errCancelled means the user dismissed the polkit prompt.
var errCancelled = errors.New("cancelled")

// pkexecElevator returns an elevator that runs `pkexec /usr/bin/rm -rf
// --one-file-system -- <path>` through bin, with an argv list and no shell.
// pkexec exits 126 when the user dismisses the dialog and 127 when it is not
// authorised or cannot authenticate (e.g. no agent); only 126 is silent.
func pkexecElevator(bin string) func(string) error {
	return func(path string) error {
		cmd := exec.Command(bin, "/usr/bin/rm", "-rf", "--one-file-system", "--", path)
		var stderr bytes.Buffer
		cmd.Stderr = &stderr
		err := cmd.Run()
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			if ee.ExitCode() == 126 {
				return errCancelled
			}
			if msg := strings.TrimSpace(stderr.String()); msg != "" {
				return errors.New(msg)
			}
		}
		return err
	}
}

// defaultPkexec is pkexec on PATH; DISKMAP_PKEXEC swaps in a stand-in so the
// UI end-to-end test never pops a real polkit prompt.
func defaultPkexec() string {
	if b := os.Getenv("DISKMAP_PKEXEC"); b != "" {
		return b
	}
	return "pkexec"
}
