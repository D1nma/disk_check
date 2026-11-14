//go:build linux

package scanner

import (
	"os"
	"strconv"
	"strings"
)

// mountEntry is one line of /proc/self/mountinfo.
type mountEntry struct {
	path   string // mount point, unescaped, no trailing slash (except "/")
	fsType string // filesystem type after the " - " separator
}

var mountinfoPath = "/proc/self/mountinfo"

// loadMountTable parses mountinfo. Field 5 of each line is the mount point,
// with octal escapes (\040 space, \011 tab, \012 newline, \134 backslash);
// the first field after the " - " separator is the filesystem type.
func loadMountTable() []mountEntry {
	data, err := os.ReadFile(mountinfoPath)
	if err != nil {
		return nil
	}
	var table []mountEntry
	for _, line := range strings.Split(string(data), "\n") {
		fields := strings.Split(line, " ")
		if len(fields) < 5 {
			continue
		}
		mp, ok := unescapeMountPath(fields[4])
		if !ok || !strings.HasPrefix(mp, "/") {
			continue
		}
		if mp != "/" {
			mp = strings.TrimSuffix(mp, "/")
		}
		fsType := ""
		for i := 5; i+1 < len(fields); i++ {
			if fields[i] == "-" {
				fsType = fields[i+1]
				break
			}
		}
		table = append(table, mountEntry{path: mp, fsType: fsType})
	}
	return table
}

// loadMounts returns the set of absolute mount-point paths, or nil when
// mountinfo is unavailable (callers then degrade to statfs per directory).
func loadMounts() map[string]struct{} {
	table := loadMountTable()
	if len(table) == 0 {
		return nil
	}
	set := make(map[string]struct{}, len(table))
	for _, m := range table {
		set[m.path] = struct{}{}
	}
	return set
}

// unescapeMountPath decodes the octal escapes used in mountinfo. It returns
// ok=false on malformed input rather than guessing.
func unescapeMountPath(s string) (string, bool) {
	if !strings.ContainsRune(s, '\\') {
		return s, true
	}
	var b strings.Builder
	b.Grow(len(s))
	for i := 0; i < len(s); {
		if s[i] != '\\' {
			b.WriteByte(s[i])
			i++
			continue
		}
		if i+3 >= len(s) {
			return "", false
		}
		v, err := strconv.ParseUint(s[i+1:i+4], 8, 8)
		if err != nil {
			return "", false
		}
		b.WriteByte(byte(v))
		i += 4
	}
	return b.String(), true
}
