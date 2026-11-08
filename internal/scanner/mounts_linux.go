//go:build linux

package scanner

import (
	"os"
	"strconv"
	"strings"
)

// loadMounts returns the set of absolute mount-point paths from
// /proc/self/mountinfo, or nil when unavailable (callers then degrade to
// statfs per directory). Field 5 of each line is the mount point, with
// octal escapes (\040 space, \011 tab, \012 newline, \134 backslash).
func loadMounts() map[string]struct{} {
	data, err := os.ReadFile("/proc/self/mountinfo")
	if err != nil {
		return nil
	}
	set := make(map[string]struct{})
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
		set[mp] = struct{}{}
	}
	if len(set) == 0 {
		return nil
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
