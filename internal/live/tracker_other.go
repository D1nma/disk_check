//go:build !linux

package live

import "time"

// Poll needs Linux /proc: elsewhere it always returns ErrUnsupported. The
// TUI should hide the writers/files panel when it gets that error.
func (t *Tracker) Poll(now time.Time) (Status, error) {
	return Status{}, ErrUnsupported
}
