// Package trash moves paths into the freedesktop.org Trash (so file managers
// can restore them) or deletes them permanently.
package trash

import (
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// homeTrash is $XDG_DATA_HOME/Trash, defaulting to ~/.local/share/Trash.
func homeTrash() (string, error) {
	data := os.Getenv("XDG_DATA_HOME")
	if data == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		data = filepath.Join(home, ".local", "share")
	}
	return filepath.Join(data, "Trash"), nil
}

func devOf(path string) (uint64, error) {
	var st syscall.Stat_t
	if err := syscall.Lstat(path, &st); err != nil {
		return 0, err
	}
	return st.Dev, nil
}

// topdir returns the mountpoint that holds path: the highest ancestor that
// is still on the same device.
func topdir(path string, dev uint64) string {
	dir := filepath.Dir(path)
	for dir != "/" {
		up := filepath.Dir(dir)
		if d, err := devOf(up); err != nil || d != dev {
			break
		}
		dir = up
	}
	return dir
}

// trashDir picks the trash for an item on device dev, per the spec: the home
// trash when on the same device, else $topdir/.Trash/$uid (only when .Trash
// is a sticky, non-symlink dir) or $topdir/.Trash-$uid. It never copies
// across devices.
func trashDir(path string, dev uint64) (string, error) {
	home, err := homeTrash()
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(home, 0o700); err != nil {
		return "", err
	}
	if hd, err := devOf(home); err == nil && hd == dev {
		return home, nil
	}
	top := topdir(path, dev)
	uid := strconv.Itoa(os.Getuid())
	shared := filepath.Join(top, ".Trash")
	if fi, err := os.Lstat(shared); err == nil && fi.IsDir() && fi.Mode()&os.ModeSticky != 0 {
		d := filepath.Join(shared, uid)
		if err := os.MkdirAll(d, 0o700); err == nil {
			return d, nil
		}
	}
	d := filepath.Join(top, ".Trash-"+uid)
	if err := os.Mkdir(d, 0o700); err != nil && !errors.Is(err, os.ErrExist) {
		return "", fmt.Errorf("no trash on %s: %w", top, err)
	}
	if fi, err := os.Lstat(d); err != nil || !fi.IsDir() {
		return "", fmt.Errorf("no usable trash on %s", top)
	}
	return d, nil
}

// encodePath percent-encodes a path the way trashinfo Path= expects.
func encodePath(p string) string {
	parts := strings.Split(p, "/")
	for i, s := range parts {
		parts[i] = url.PathEscape(s)
	}
	return strings.Join(parts, "/")
}

// Trash moves path (never following a final symlink) into the trash and
// writes its .trashinfo. On failure nothing is left behind.
func Trash(path string) error {
	path, err := filepath.Abs(path)
	if err != nil {
		return err
	}
	dev, err := devOf(path)
	if err != nil {
		return err
	}
	dir, err := trashDir(path, dev)
	if err != nil {
		return err
	}
	files, info := filepath.Join(dir, "files"), filepath.Join(dir, "info")
	for _, d := range []string{files, info} {
		if err := os.MkdirAll(d, 0o700); err != nil {
			return err
		}
	}
	base := filepath.Base(path)
	body := fmt.Sprintf("[Trash Info]\nPath=%s\nDeletionDate=%s\n",
		encodePath(path), time.Now().Format("2006-01-02T15:04:05"))
	for i := 1; i < 10000; i++ {
		name := base
		if i > 1 {
			name = base + "." + strconv.Itoa(i)
		}
		infoPath := filepath.Join(info, name+".trashinfo")
		// O_EXCL on the info file is the spec's lock for the name.
		f, err := os.OpenFile(infoPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
		if errors.Is(err, os.ErrExist) {
			continue
		}
		if err != nil {
			return err
		}
		_, werr := f.WriteString(body)
		cerr := f.Close()
		if werr != nil || cerr != nil {
			_ = os.Remove(infoPath)
			return errors.Join(werr, cerr)
		}
		dst := filepath.Join(files, name)
		if _, err := os.Lstat(dst); err == nil {
			_ = os.Remove(infoPath) // orphan in files/: try the next name
			continue
		}
		if err := os.Rename(path, dst); err != nil {
			_ = os.Remove(infoPath)
			return err
		}
		return nil
	}
	return fmt.Errorf("no free trash name for %s", base)
}

// Delete removes path permanently. A directory is removed recursively; an
// error means some of it may remain.
func Delete(path string) error {
	if _, err := os.Lstat(path); err != nil {
		return err
	}
	return os.RemoveAll(path)
}
