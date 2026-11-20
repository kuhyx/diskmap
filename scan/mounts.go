package scan

import (
	"bufio"
	"io"
	"os"
	"strings"
)

// pseudoFS lists filesystem types that hold no user data (or duplicate data
// that lives elsewhere, like docker overlays). They are skipped even with
// --cross, so the totals only ever count real disk usage.
var pseudoFS = map[string]bool{
	"proc": true, "sysfs": true, "devtmpfs": true, "devpts": true,
	"tmpfs": true, "ramfs": true, "cgroup": true, "cgroup2": true,
	"securityfs": true, "debugfs": true, "tracefs": true, "configfs": true,
	"fusectl": true, "pstore": true, "bpf": true, "mqueue": true,
	"hugetlbfs": true, "autofs": true, "binfmt_misc": true, "efivarfs": true,
	"nsfs": true, "overlay": true, "rpc_pipefs": true, "fuse.portal": true,
	"fuse.gvfsd-fuse": true, "selinuxfs": true,
}

// mounts maps every mountpoint path to its filesystem type.
type mounts map[string]string

// readMounts parses /proc/self/mountinfo; a missing file yields no mounts,
// which leaves the st_dev check as the only boundary.
func readMounts() mounts {
	f, err := os.Open("/proc/self/mountinfo")
	if err != nil {
		return mounts{}
	}
	defer f.Close()
	return parseMountinfo(f)
}

func parseMountinfo(r io.Reader) mounts {
	m := mounts{}
	sc := bufio.NewScanner(r)
	for sc.Scan() {
		fields := strings.Fields(sc.Text())
		sep := -1
		for i, f := range fields {
			if f == "-" {
				sep = i
				break
			}
		}
		if len(fields) < 5 || sep < 0 || sep+1 >= len(fields) {
			continue
		}
		m[unescapeMount(fields[4])] = fields[sep+1]
	}
	return m
}

// unescapeMount decodes the octal escapes (\040 = space) mountinfo uses.
func unescapeMount(s string) string {
	if !strings.Contains(s, `\`) {
		return s
	}
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] == '\\' && i+3 < len(s) {
			v := (s[i+1]-'0')*64 + (s[i+2]-'0')*8 + (s[i+3] - '0')
			b.WriteByte(v)
			i += 3
			continue
		}
		b.WriteByte(s[i])
	}
	return b.String()
}

// skip reports whether the directory at path is a mountpoint the scan must
// not enter: pseudo filesystems always, real ones unless cross is set.
func (m mounts) skip(path string, cross bool) bool {
	if path == "/" {
		return false
	}
	fs, ok := m[path]
	if !ok {
		return false
	}
	return pseudoFS[fs] || !cross
}
