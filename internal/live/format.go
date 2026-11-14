package live

import (
	"math"
	"strconv"
	"time"

	"github.com/D1nma/disk_check/internal/textsafe"
)

// stableBps is the |rate| under which Fill.String calls a filesystem
// stable: metadata and journal traffic make the measured rate never exactly 0.
const stableBps = 4096

// FormatRate renders a rate in bytes per second with IEC units, like
// display.FormatSize: "45.0 MiB/s", "-512 B/s".
func FormatRate(bps float64) string {
	if math.IsNaN(bps) || math.IsInf(bps, 0) {
		return "? B/s"
	}
	sign := ""
	if bps < 0 {
		sign, bps = "-", -bps
	}
	units := [...]string{"B/s", "KiB/s", "MiB/s", "GiB/s", "TiB/s"}
	i := 0
	for bps >= 1024 && i < len(units)-1 {
		bps /= 1024
		i++
	}
	if i == 0 {
		return sign + strconv.FormatFloat(bps, 'f', 0, 64) + " " + units[0]
	}
	return sign + strconv.FormatFloat(bps, 'f', 1, 64) + " " + units[i]
}

// FormatETA renders a time to full compactly: "45s", "38m", "2h05m",
// "3d04h". It returns "" for d <= 0 (unknown).
func FormatETA(d time.Duration) string {
	if d <= 0 {
		return ""
	}
	s := int64(d.Round(time.Second) / time.Second)
	switch {
	case s < 60:
		return strconv.FormatInt(max(s, 1), 10) + "s"
	case s < 3600:
		return strconv.FormatInt(s/60, 10) + "m"
	case s < 86400:
		return strconv.FormatInt(s/3600, 10) + "h" + pad2(s%3600/60) + "m"
	default:
		return strconv.FormatInt(s/86400, 10) + "d" + pad2(s%86400/3600) + "h"
	}
}

func pad2(n int64) string {
	if n < 10 {
		return "0" + strconv.FormatInt(n, 10)
	}
	return strconv.FormatInt(n, 10)
}

// String summarises the fill state: "filling at 45.0 MiB/s, full in 38m".
func (f Fill) String() string {
	switch {
	case !f.Valid:
		return "measuring fill rate…"
	case f.RateBps >= stableBps:
		s := "filling at " + FormatRate(f.RateBps)
		if f.Avail <= 0 {
			return s + ", full"
		}
		if f.ETA > 0 {
			return s + ", full in " + FormatETA(f.ETA)
		}
		return s
	case f.RateBps <= -stableBps:
		return "freeing at " + FormatRate(-f.RateBps)
	default:
		return "stable"
	}
}

// String renders the writer with its name cleaned for the terminal:
// "java (pid 1234) 20.0 MiB/s".
func (w Writer) String() string {
	return textsafe.Clean(w.Comm) + " (pid " + strconv.Itoa(w.PID) + ") " + FormatRate(w.WriteBps)
}

// String renders the file with its path and process name cleaned for the
// terminal: "/var/log/app.log +20.0 MiB/s — java pid 1234".
func (g GrowingFile) String() string {
	p := textsafe.Clean(g.Path)
	if g.Deleted {
		p += " (deleted)"
	}
	return p + " +" + FormatRate(g.GrowthBps) + " — " + textsafe.Clean(g.Comm) + " pid " + strconv.Itoa(g.PID)
}
