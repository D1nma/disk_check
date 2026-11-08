//go:build !linux

package scanner

// Without /proc/self/mountinfo the scanner falls back to statfs on every
// directory, matching the historical behavior.
func loadMounts() map[string]struct{} { return nil }
