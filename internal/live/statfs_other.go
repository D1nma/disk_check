//go:build !linux && !darwin

package live

import "fmt"

func statfsUsage(path string) (used, avail int64, err error) {
	return 0, 0, fmt.Errorf("live: statfs %s: %w", path, ErrUnsupported)
}
