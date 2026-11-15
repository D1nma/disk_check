package live

import (
	"errors"
	"fmt"
	"path/filepath"
	"time"
)

// ErrUnsupported is returned by Tracker.Poll on systems without Linux /proc
// (and by FillMeter.Sample where statfs is not implemented). It matches
// errors.ErrUnsupported with errors.Is.
var ErrUnsupported = fmt.Errorf("live: not supported on this system: %w", errors.ErrUnsupported)

const (
	// MaxWriters bounds Status.Writers; only these processes get their
	// open files inspected.
	MaxWriters = 10
	// MaxFiles bounds Status.Files.
	MaxFiles = 15
)

// GrowingFile is a regular file, open by a top writer, whose allocated size
// grew since the previous poll.
type GrowingFile struct {
	Path      string  // readlink target; " (deleted)" suffix removed when Deleted
	Deleted   bool    // the file has no name left (st_nlink == 0): space is freed only when it is closed
	Size      int64   // allocated bytes, st_blocks * 512
	GrowthBps float64 // allocated-bytes growth rate since the previous poll
	PID       int     // a process holding the file open (the top writer among them)
	Comm      string  // that process's name
	FD        int     // the descriptor number in that process
}

// Writer is a process whose write_bytes counter grew since the previous poll.
type Writer struct {
	PID      int
	Comm     string
	WriteBps float64 // /proc/PID/io write_bytes delta / dt, all filesystems: a hint, not exact
}

// Status is the result of one Tracker poll.
type Status struct {
	Writers []Writer      // sorted by WriteBps desc, only > 0, at most MaxWriters
	Files   []GrowingFile // regular files on the watched device that grew, by GrowthBps desc, at most MaxFiles
	Hidden  int           // processes whose io or fd could not be read (permission): run as root to see them
	Warmup  bool          // true on the first poll, or when now is not after the previous one: baselines only, no rates
}

// fileKey identifies an open file across polls.
type fileKey struct {
	dev, ino uint64
}

// procEntry is what a Tracker remembers about a process between polls.
type procEntry struct {
	start      uint64 // starttime (field 22 of /proc/PID/stat): tells a reused pid apart
	writeBytes uint64
}

// Tracker polls /proc for the processes writing the most and the files they
// are growing. It keeps the previous write_bytes of every process (keyed by
// pid and start time) and the previous allocated size of every file it
// inspected (keyed by device and inode), so each Poll reports rates since
// the one before. Only the state seen at the last poll is kept: vanished
// processes and files that are no longer inspected are forgotten.
type Tracker struct {
	procRoot string
	dev      uint64
	last     time.Time

	// Double-buffered state: Poll fills next* from the current readings,
	// compares them with the previous ones, then swaps.
	procs, nextProcs map[int]procEntry
	files, nextFiles map[fileKey]int64

	buf     []byte   // reused /proc file read buffer
	linkBuf []byte   // reused readlink buffer
	cands   []Writer // reused candidate list

	// readFile reads a small /proc file into buf; nil means readSmall.
	// Tests override it to inject errors.
	readFile func(path string, buf []byte) ([]byte, error)
}

// NewTracker returns a Tracker reading procRoot ("/proc" in production;
// empty means "/proc") and reporting growing files on the device dev, the
// st_dev of the watched filesystem as uint64(stat.Dev) (0 reports files on
// every device).
//
// Note: on btrfs every subvolume has its own st_dev, so files in another
// subvolume of the same filesystem are filtered out.
func NewTracker(procRoot string, dev uint64) *Tracker {
	if procRoot == "" {
		procRoot = "/proc"
	}
	return &Tracker{
		procRoot:  filepath.Clean(procRoot),
		dev:       dev,
		procs:     make(map[int]procEntry),
		nextProcs: make(map[int]procEntry),
		files:     make(map[fileKey]int64),
		nextFiles: make(map[fileKey]int64),
	}
}

// usage converts statfs block counts to used and available bytes, clamping
// inconsistent counts (free > total) to zero rather than going negative.
func usage(blocks, bfree, bavail uint64, bsize int64) (used, avail int64) {
	if bsize <= 0 {
		return 0, 0
	}
	if bfree < blocks {
		used = int64(blocks-bfree) * bsize
	}
	return used, int64(bavail) * bsize
}
