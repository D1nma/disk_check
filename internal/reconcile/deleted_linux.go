//go:build linux

package reconcile

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"golang.org/x/sys/unix"
)

const deletedSuffix = " (deleted)"

type fileKey struct{ dev, ino uint64 }

// DeletedOpen scans procRoot ("/proc" in production) and returns
// deleted-but-open regular files on filesystem dev (dev==0: all
// filesystems), deduplicated by (dev, ino), sorted by Size desc then Path.
// Processes it cannot inspect (EACCES, vanished) are skipped silently; the
// int return is how many processes were skipped for permission reasons (so
// the UI can say "relancez en root pour tout voir").
//
// A file counts only when its link count is 0: a name ending in
// " (deleted)" whose inode is still linked elsewhere (a hard link, or a
// file literally named "x (deleted)") is still visible to the scan and
// frees nothing when closed.
func DeletedOpen(procRoot string, dev uint64) ([]DeletedFile, int, error) {
	names, err := readNames(procRoot)
	if err != nil {
		return nil, 0, err
	}
	// Visit PIDs, then fds, in numeric order so the first holder seen is
	// the lowest (pid, fd): the kept holder is deterministic.
	pids := numericNames(names)

	found := make(map[fileKey]*DeletedFile)
	hidden := 0
	for _, pid := range pids {
		if scanPID(filepath.Join(procRoot, strconv.Itoa(pid)), pid, dev, found) {
			hidden++
		}
	}

	out := make([]DeletedFile, 0, len(found))
	for _, d := range found {
		out = append(out, *d)
	}
	sortDeleted(out)
	return out, hidden, nil
}

// scanPID records the deleted-but-open files of one process into found.
// It reports whether part of the process could not be inspected for lack
// of permission; a process that vanished mid-scan is not "denied".
func scanPID(pidDir string, pid int, dev uint64, found map[fileKey]*DeletedFile) (denied bool) {
	fdDir := filepath.Join(pidDir, "fd")
	names, err := readNames(fdDir)
	if err != nil {
		return isPermission(err)
	}
	comm, haveComm := "", false
	for _, fd := range numericNames(names) {
		link := filepath.Join(fdDir, strconv.Itoa(fd))
		target, err := os.Readlink(link)
		if err != nil {
			denied = denied || isPermission(err)
			continue
		}
		path, ok := deletedPath(target)
		if !ok {
			continue
		}
		// Stat follows the magic link to the open inode itself.
		var st unix.Stat_t
		if err := unix.Stat(link, &st); err != nil {
			denied = denied || isPermission(err)
			continue
		}
		if st.Mode&unix.S_IFMT != unix.S_IFREG || st.Nlink != 0 {
			continue
		}
		k := fileKey{dev: uint64(st.Dev), ino: uint64(st.Ino)}
		if dev != 0 && k.dev != dev {
			continue
		}
		if d, ok := found[k]; ok {
			d.Holders++
			continue
		}
		if !haveComm {
			comm, haveComm = readComm(pidDir), true
		}
		found[k] = &DeletedFile{
			PID:     pid,
			Comm:    comm,
			FD:      fd,
			Holders: 1,
			Path:    path,
			Size:    int64(st.Blocks) * 512,
			Dev:     k.dev,
			Ino:     k.ino,
		}
	}
	return denied
}

// deletedPath returns the original path of a deleted file from an fd link
// target, or ok=false for anything else: live files, sockets, pipes and
// anon inodes (no leading "/"), and memfds, which live on an internal
// tmpfs and read as "/memfd:name (deleted)".
func deletedPath(target string) (string, bool) {
	if !strings.HasSuffix(target, deletedSuffix) ||
		!strings.HasPrefix(target, "/") ||
		strings.HasPrefix(target, "/memfd:") {
		return "", false
	}
	return strings.TrimSuffix(target, deletedSuffix), true
}

func readComm(pidDir string) string {
	b, err := os.ReadFile(filepath.Join(pidDir, "comm"))
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(b))
}

// readNames lists a directory without stat-ing its entries.
func readNames(dir string) ([]string, error) {
	f, err := os.Open(dir)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return f.Readdirnames(-1)
}

// numericNames keeps the non-negative integer names, sorted ascending.
func numericNames(names []string) []int {
	nums := make([]int, 0, len(names))
	for _, n := range names {
		if v, err := strconv.Atoi(n); err == nil && v >= 0 {
			nums = append(nums, v)
		}
	}
	sort.Ints(nums)
	return nums
}

func isPermission(err error) bool {
	return errors.Is(err, fs.ErrPermission)
}
