//go:build !linux && !darwin

package reconcile

import (
	"errors"
	"fmt"
	"runtime"
)

// Stat is only implemented on Linux and macOS.
func Stat(path string) (FSInfo, error) {
	return FSInfo{}, fmt.Errorf("informations du système de fichiers : non disponible sur %s : %w",
		runtime.GOOS, errors.ErrUnsupported)
}
