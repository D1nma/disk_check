//go:build linux

package reconcile

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"golang.org/x/sys/unix"
)

const mountinfoPath = "/proc/self/mountinfo"

// Stat describes the filesystem holding path: df figures from statfs(2),
// mount point, source and type from /proc/self/mountinfo.
func Stat(path string) (FSInfo, error) {
	return statWith(path, mountinfoPath)
}

// statWith is Stat with an injectable mountinfo file, for tests.
func statWith(path, mountinfo string) (FSInfo, error) {
	abs := canonical(path)
	var sfs unix.Statfs_t
	if err := unix.Statfs(abs, &sfs); err != nil {
		return FSInfo{}, &os.PathError{Op: "statfs", Path: path, Err: err}
	}
	// df counts blocks in f_frsize units; the kernel sets it to f_bsize
	// when a filesystem leaves it 0, but stay safe on odd ones.
	bs := int64(sfs.Frsize)
	if bs <= 0 {
		bs = int64(sfs.Bsize)
	}
	info := fromCounts(uint64(sfs.Blocks), uint64(sfs.Bfree), uint64(sfs.Bavail), bs)
	info.Inodes, info.InodesFree = uint64(sfs.Files), uint64(sfs.Ffree)

	if m, ok := findMount(mountinfo, abs); ok {
		info.MountPoint, info.Source, info.FSType = m.point, m.source, m.fstype
	} else {
		info.MountPoint = mountByDev(abs)
	}

	var st unix.Stat_t
	if err := unix.Stat(info.MountPoint, &st); err == nil {
		info.Dev = uint64(st.Dev)
	} else if err := unix.Stat(abs, &st); err == nil {
		info.Dev = uint64(st.Dev)
	}
	return info, nil
}

type mountEntry struct {
	point, source, fstype string
}

// findMount returns the mountinfo entry whose mount point is the longest
// path-prefix of abs.
func findMount(mountinfo, abs string) (mountEntry, bool) {
	data, err := os.ReadFile(mountinfo)
	if err != nil {
		return mountEntry{}, false
	}
	return longestMount(parseMountinfo(string(data)), abs)
}

// parseMountinfo extracts mount point, source and type from each line of
// a mountinfo file (proc(5)):
//
//	36 35 98:0 /mnt1 /mnt2 rw,noatime master:1 - ext3 /dev/root rw,errors=continue
//	(1)(2) (3)   (4)   (5)      (6)      (7)   (8) (9)   (10)         (11)
//
// Field 7 is a variable-length list of optional fields ended by "-".
// Fields are split on single spaces so an empty source stays a field.
func parseMountinfo(data string) []mountEntry {
	var out []mountEntry
	for _, line := range strings.Split(data, "\n") {
		f := strings.Split(line, " ")
		sep := -1
		for i := 6; i < len(f); i++ {
			if f[i] == "-" {
				sep = i
				break
			}
		}
		if sep < 0 || sep+2 >= len(f) {
			continue
		}
		point, ok := unescapeOctal(f[4])
		if !ok || !strings.HasPrefix(point, "/") {
			continue
		}
		fstype, ok1 := unescapeOctal(f[sep+1])
		source, ok2 := unescapeOctal(f[sep+2])
		if !ok1 || !ok2 {
			continue
		}
		if source == "none" {
			source = ""
		}
		out = append(out, mountEntry{point: filepath.Clean(point), source: source, fstype: fstype})
	}
	return out
}

// longestMount picks the mount point that is the longest component-wise
// prefix of abs ("/var" contains "/var/log" but not "/variable"). On a tie
// (a mount stacked over another) the later line, the visible one, wins.
func longestMount(mounts []mountEntry, abs string) (mountEntry, bool) {
	best := -1
	for i := range mounts {
		if !pathHasPrefix(abs, mounts[i].point) {
			continue
		}
		if best < 0 || len(mounts[i].point) >= len(mounts[best].point) {
			best = i
		}
	}
	if best < 0 {
		return mountEntry{}, false
	}
	return mounts[best], true
}

// pathHasPrefix reports whether prefix is p or one of its ancestors. Both
// must be clean absolute paths.
func pathHasPrefix(p, prefix string) bool {
	if prefix == "/" {
		return strings.HasPrefix(p, "/")
	}
	return p == prefix || (strings.HasPrefix(p, prefix) && p[len(prefix)] == '/')
}

// unescapeOctal decodes the \ooo escapes of mountinfo (\040 space, \011
// tab, \012 newline, \134 backslash). Malformed input yields ok=false
// rather than a guess.
func unescapeOctal(s string) (string, bool) {
	if !strings.Contains(s, `\`) {
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
		if i+4 > len(s) {
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

// mountByDev finds the mount point of abs without mountinfo: the highest
// ancestor still on the same device. A bind mount from the same device is
// invisible this way, hence only a fallback.
func mountByDev(abs string) string {
	var st unix.Stat_t
	if unix.Stat(abs, &st) != nil {
		return "/"
	}
	dev := uint64(st.Dev)
	cur := abs
	for cur != "/" {
		parent := filepath.Dir(cur)
		if unix.Stat(parent, &st) != nil || uint64(st.Dev) != dev {
			return cur
		}
		cur = parent
	}
	return "/"
}
