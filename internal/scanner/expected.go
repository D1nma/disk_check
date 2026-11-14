package scanner

import (
	"strings"

	"golang.org/x/sys/unix"
)

// pseudoFSTypes are mount types that hold no user data (or would trigger an
// automount when stat-ed); they never contribute to the expected count.
var pseudoFSTypes = map[string]bool{
	"proc": true, "sysfs": true, "cgroup": true, "cgroup2": true, "debugfs": true,
	"tracefs": true, "securityfs": true, "pstore": true, "bpf": true, "configfs": true,
	"fusectl": true, "mqueue": true, "hugetlbfs": true, "binfmt_misc": true,
	"devpts": true, "nsfs": true, "efivarfs": true, "rpc_pipefs": true, "nfsd": true,
	"autofs": true, "selinuxfs": true, "ramfs": true,
}

// expectedEntries estimates how many entries (files + directories) the scan
// of abs will visit, from the used-inode counts of the filesystems it covers.
// It only answers when abs is a mount point: below one, statfs describes the
// whole filesystem, not the subtree. It returns 0 when unknown (for example
// btrfs, which reports no inode counts).
func expectedEntries(abs string, sameDevice bool, table []mountEntry, excludes []string) int64 {
	isMountPoint := abs == "/"
	for _, m := range table {
		if m.path == abs {
			isMountPoint = true
			break
		}
	}
	if !isMountPoint {
		return 0
	}
	if sameDevice || len(table) == 0 {
		used, ok := usedInodes(abs)
		if !ok {
			return 0
		}
		return used
	}

	// Global mode: every real filesystem mounted at or below abs. Bind and
	// overlay mounts report the statistics of the filesystem they expose, so
	// identical statfs tuples are counted once.
	type fsKey struct{ blocks, bfree, files, ffree uint64 }
	seen := make(map[fsKey]bool)
	var total int64
	prefix := abs
	if prefix != "/" {
		prefix += "/"
	}
	for _, m := range table {
		if m.path != abs && !strings.HasPrefix(m.path, prefix) {
			continue
		}
		if pseudoFSTypes[m.fsType] || isExcluded(m.path, excludes) {
			continue
		}
		var st unix.Statfs_t
		if err := unix.Statfs(m.path, &st); err != nil {
			continue
		}
		if isKernFS(int64(st.Type)) {
			continue
		}
		files, ffree := uint64(st.Files), uint64(st.Ffree)
		if files == 0 {
			return 0 // no inode accounting on this filesystem: no reliable total
		}
		k := fsKey{uint64(st.Blocks), uint64(st.Bfree), files, ffree}
		if seen[k] {
			continue
		}
		seen[k] = true
		total += int64(files - ffree)
	}
	return total
}

func usedInodes(path string) (int64, bool) {
	var st unix.Statfs_t
	if err := unix.Statfs(path, &st); err != nil {
		return 0, false
	}
	files, ffree := uint64(st.Files), uint64(st.Ffree)
	if files == 0 || ffree > files {
		return 0, false
	}
	return int64(files - ffree), true
}
