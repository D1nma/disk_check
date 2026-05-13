package display

import "fmt"

func FormatSize(size int64) string {
	units := []string{"B", "KiB", "MiB", "GiB", "TiB"}
	f := float64(size)
	i := 0
	for f >= 1024 && i < len(units)-1 {
		f /= 1024
		i++
	}
	return fmt.Sprintf("%.1f %s", f, units[i])
}
