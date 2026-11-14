//go:build !linux

package scanner

// mountEntry mirrors the Linux definition; there is no mountinfo here.
type mountEntry struct {
	path   string
	fsType string
}

func loadMountTable() []mountEntry { return nil }

// Without /proc/self/mountinfo the scanner falls back to statfs on every
// directory, matching the historical behavior.
func loadMounts() map[string]struct{} { return nil }
