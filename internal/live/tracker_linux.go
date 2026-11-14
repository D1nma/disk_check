//go:build linux

package live

import (
	"bytes"
	"cmp"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"slices"
	"strconv"
	"strings"
	"time"

	"golang.org/x/sys/unix"
)

// maxFDsPerProc bounds how many descriptors of one writer are inspected per
// poll. Descriptors are allocated lowest-first and listed in order, so log
// files opened at start-up come first; a process with tens of thousands of
// sockets must not make every poll cost tens of thousands of syscalls.
const maxFDsPerProc = 4096

// Poll reads /proc and returns the processes whose write_bytes grew since
// the previous poll and the files those processes grew.
//
// Cost: /proc/PID/io is read for every process; /proc/PID/stat only for a
// process that is new or whose counter changed (to check its start time and
// get its name). Only the top MaxWriters writers have their fd/ directory
// listed and their open files stat'ed.
//
// Rates need two polls: the first poll (and any poll whose now is not after
// the previous one) only records baselines and returns Warmup. A file is
// reported from the second time it is seen, so it appears one poll after
// its process enters the top writers; a writer whose rate drops out of the
// top for one poll loses its files' baselines and they reappear a poll
// later. Processes holding a growing file are not inspected unless they are
// among the top writers.
//
// The returned error is non-nil only when procRoot itself cannot be listed;
// processes that vanish or cannot be read are skipped (the unreadable ones
// are counted in Status.Hidden).
func (t *Tracker) Poll(now time.Time) (Status, error) {
	names, err := readDirNames(t.procRoot, -1)
	if err != nil && len(names) == 0 {
		return Status{}, fmt.Errorf("live: %w", err)
	}
	if t.buf == nil {
		t.buf = make([]byte, 4096)
		t.linkBuf = make([]byte, unix.PathMax+1)
	}

	warm := t.last.IsZero() || !now.After(t.last)
	var dt float64
	if !warm {
		dt = now.Sub(t.last).Seconds()
	}
	st := Status{Warmup: warm}

	clear(t.nextProcs)
	t.cands = t.cands[:0]
	for _, name := range names {
		pid, ok := parseNum(name)
		if !ok {
			continue
		}
		dir := t.procRoot + "/" + name
		data, err := t.read(dir + "/io")
		if err != nil {
			if errors.Is(err, fs.ErrPermission) {
				st.Hidden++
			}
			continue // vanished, or not a process directory
		}
		wb, ok := parseWriteBytes(data)
		if !ok {
			continue
		}
		prev, known := t.procs[pid]
		if known && wb == prev.writeBytes {
			// Idle since the last poll: whether or not the pid was reused
			// meanwhile, its rate is 0. Carry the entry over without
			// reading stat; a reused pid is caught when it next writes.
			t.nextProcs[pid] = prev
			continue
		}
		data, err = t.read(dir + "/stat")
		if err != nil {
			continue
		}
		start, comm, ok := parseStat(data)
		if !ok {
			continue
		}
		t.nextProcs[pid] = procEntry{start: start, writeBytes: wb}
		if warm || !known || prev.start != start || wb < prev.writeBytes {
			continue // no baseline for this process (new, reused pid, reset counter)
		}
		t.cands = append(t.cands, Writer{
			PID:      pid,
			Comm:     string(comm), // copy: comm points into t.buf
			WriteBps: float64(wb-prev.writeBytes) / dt,
		})
	}
	t.procs, t.nextProcs = t.nextProcs, t.procs

	slices.SortFunc(t.cands, func(a, b Writer) int {
		if c := cmp.Compare(b.WriteBps, a.WriteBps); c != 0 {
			return c
		}
		return cmp.Compare(a.PID, b.PID)
	})
	if n := min(len(t.cands), MaxWriters); n > 0 {
		st.Writers = slices.Clone(t.cands[:n])
	}

	clear(t.nextFiles)
	for _, w := range st.Writers {
		st.Files = t.scanFDs(w, dt, st.Files, &st.Hidden)
	}
	t.files, t.nextFiles = t.nextFiles, t.files
	t.last = now

	slices.SortFunc(st.Files, func(a, b GrowingFile) int {
		if c := cmp.Compare(b.GrowthBps, a.GrowthBps); c != 0 {
			return c
		}
		if c := strings.Compare(a.Path, b.Path); c != 0 {
			return c
		}
		return cmp.Compare(a.PID, b.PID)
	})
	if len(st.Files) > MaxFiles {
		clear(st.Files[MaxFiles:])
		st.Files = st.Files[:MaxFiles]
	}
	return st, nil
}

// scanFDs inspects the open descriptors of writer w, records the allocated
// size of every regular file on the watched device in t.nextFiles and
// appends to out the ones that grew since the previous poll. A file open
// several times (several fds, or several writers) is reported once, for the
// first writer in rate order.
func (t *Tracker) scanFDs(w Writer, dt float64, out []GrowingFile, hidden *int) []GrowingFile {
	fdDir := t.procRoot + "/" + strconv.Itoa(w.PID) + "/fd"
	names, err := readDirNames(fdDir, maxFDsPerProc)
	if err != nil && len(names) == 0 {
		if errors.Is(err, fs.ErrPermission) {
			*hidden++
		}
		return out
	}
	for _, name := range names {
		fd, ok := parseNum(name)
		if !ok {
			continue
		}
		link := fdDir + "/" + name
		target, ok := t.readlink(link)
		if !ok || len(target) == 0 || target[0] != '/' {
			continue // socket:[…], pipe:[…], anon_inode:…
		}
		// stat follows the magic link to the open inode, even when the file
		// has been deleted.
		var sst unix.Stat_t
		if unix.Stat(link, &sst) != nil || sst.Mode&unix.S_IFMT != unix.S_IFREG {
			continue
		}
		key := fileKey{dev: uint64(sst.Dev), ino: uint64(sst.Ino)}
		if t.dev != 0 && key.dev != t.dev {
			continue
		}
		if _, dup := t.nextFiles[key]; dup {
			continue
		}
		size := int64(sst.Blocks) * 512
		t.nextFiles[key] = size
		prev, seen := t.files[key]
		if !seen || size <= prev {
			continue // first sighting (no rate yet), unchanged or shrunk
		}
		path, deleted := splitDeleted(string(target), sst.Nlink == 0)
		out = append(out, GrowingFile{
			Path:      path,
			Deleted:   deleted,
			Size:      size,
			GrowthBps: float64(size-prev) / dt,
			PID:       w.PID,
			Comm:      w.Comm,
			FD:        fd,
		})
	}
	return out
}

func (t *Tracker) read(path string) ([]byte, error) {
	if t.readFile != nil {
		return t.readFile(path, t.buf)
	}
	return readSmall(path, t.buf)
}

// readlink returns the target of link. The result aliases t.linkBuf and is
// valid until the next call.
func (t *Tracker) readlink(link string) ([]byte, bool) {
	n, err := unix.Readlink(link, t.linkBuf)
	if err != nil {
		return nil, false
	}
	if n < len(t.linkBuf) {
		return t.linkBuf[:n], true
	}
	s, err := os.Readlink(link) // longer than PATH_MAX: rare, allocate
	if err != nil {
		return nil, false
	}
	return []byte(s), true
}

// splitDeleted turns the readlink target of an open file into a path. The
// kernel appends " (deleted)" to the target of an unlinked file; the suffix
// is only stripped when the inode really has no link left, so a file
// genuinely named "x (deleted)" keeps its name.
func splitDeleted(target string, unlinked bool) (path string, deleted bool) {
	if !unlinked {
		return target, false
	}
	return strings.TrimSuffix(target, " (deleted)"), true
}

// readSmall reads a small file (a /proc pseudo-file) into buf without
// allocating an *os.File. A short read ends the loop: /proc seq files
// return their whole content in one read when it fits, which saves the
// extra read syscall per file that would otherwise only return EOF.
func readSmall(path string, buf []byte) ([]byte, error) {
	fd, err := unix.Open(path, unix.O_RDONLY|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, &fs.PathError{Op: "open", Path: path, Err: err}
	}
	defer unix.Close(fd)
	n := 0
	for n < len(buf) {
		m, err := unix.Read(fd, buf[n:])
		if err == unix.EINTR {
			continue
		}
		if err != nil {
			return nil, &fs.PathError{Op: "read", Path: path, Err: err}
		}
		n += m
		if m == 0 || n < len(buf) {
			break
		}
	}
	return buf[:n], nil
}

// readDirNames lists up to n names of dir (all when n <= 0). Names read
// before an error are returned with it.
func readDirNames(dir string, n int) ([]string, error) {
	f, err := os.Open(dir)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	names, err := f.Readdirnames(n)
	if err == io.EOF {
		err = nil
	}
	return names, err
}

// parseNum parses a /proc pid or fd directory entry name.
func parseNum(name string) (int, bool) {
	if name == "" || name[0] < '0' || name[0] > '9' {
		return 0, false
	}
	n, err := strconv.Atoi(name)
	return n, err == nil
}

// parseUint parses a decimal unsigned integer, rejecting empty input,
// non-digits and overflow.
func parseUint(b []byte) (uint64, bool) {
	if len(b) == 0 {
		return 0, false
	}
	var n uint64
	for _, c := range b {
		if c < '0' || c > '9' {
			return 0, false
		}
		d := uint64(c - '0')
		if n > (1<<64-1-d)/10 {
			return 0, false
		}
		n = n*10 + d
	}
	return n, true
}

// parseWriteBytes extracts the write_bytes counter from /proc/PID/io. The
// key must start a line: "cancelled_write_bytes:" also contains it.
func parseWriteBytes(data []byte) (uint64, bool) {
	const key = "write_bytes:"
	for len(data) > 0 {
		line := data
		if i := bytes.IndexByte(data, '\n'); i >= 0 {
			line, data = data[:i], data[i+1:]
		} else {
			data = nil
		}
		if bytes.HasPrefix(line, []byte(key)) {
			return parseUint(bytes.TrimSpace(line[len(key):]))
		}
	}
	return 0, false
}

// parseStat extracts the process name and starttime (field 22) from
// /proc/PID/stat. The name sits between the first '(' and the last ')' and
// may itself contain spaces and parentheses. The returned comm aliases data.
func parseStat(data []byte) (start uint64, comm []byte, ok bool) {
	open := bytes.IndexByte(data, '(')
	end := bytes.LastIndexByte(data, ')')
	if open < 0 || end < open {
		return 0, nil, false
	}
	comm = data[open+1 : end]
	// Fields after the name start at field 3 (state): starttime, field 22,
	// is the 20th of them.
	rest := data[end+1:]
	field := 0
	for i := 0; i < len(rest); {
		for i < len(rest) && (rest[i] == ' ' || rest[i] == '\n') {
			i++
		}
		j := i
		for j < len(rest) && rest[j] != ' ' && rest[j] != '\n' {
			j++
		}
		if j == i {
			break
		}
		field++
		if field == 20 {
			start, ok = parseUint(rest[i:j])
			return start, comm, ok
		}
		i = j
	}
	return 0, nil, false
}
