//go:build !linux

package reconcile

import (
	"errors"
	"fmt"
	"runtime"
)

// DeletedOpen needs Linux's /proc/PID/fd; elsewhere it reports
// errors.ErrUnsupported.
func DeletedOpen(procRoot string, dev uint64) ([]DeletedFile, int, error) {
	return nil, 0, fmt.Errorf("fichiers supprimés encore ouverts : non disponible sur %s : %w",
		runtime.GOOS, errors.ErrUnsupported)
}
