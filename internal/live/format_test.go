package live

import (
	"math"
	"strings"
	"testing"
	"time"
)

func TestFormatRate(t *testing.T) {
	cases := []struct {
		in   float64
		want string
	}{
		{0, "0 B/s"},
		{512, "512 B/s"},
		{1536, "1.5 KiB/s"},
		{45 * mib, "45.0 MiB/s"},
		{-2048, "-2.0 KiB/s"},
		{3 << 40, "3.0 TiB/s"},
		{math.NaN(), "? B/s"},
	}
	for _, c := range cases {
		if got := FormatRate(c.in); got != c.want {
			t.Errorf("FormatRate(%v) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestFormatETA(t *testing.T) {
	cases := []struct {
		in   time.Duration
		want string
	}{
		{0, ""},
		{-time.Second, ""},
		{300 * time.Millisecond, "1s"},
		{45 * time.Second, "45s"},
		{38*time.Minute + 20*time.Second, "38m"},
		{2*time.Hour + 5*time.Minute, "2h05m"},
		{3*24*time.Hour + 4*time.Hour + 10*time.Minute, "3d04h"},
	}
	for _, c := range cases {
		if got := FormatETA(c.in); got != c.want {
			t.Errorf("FormatETA(%v) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestFillString(t *testing.T) {
	cases := []struct {
		f    Fill
		want string
	}{
		{Fill{}, "measuring fill rate…"},
		{Fill{Valid: true, RateBps: 45 * mib, Avail: 1 << 30, ETA: 38 * time.Minute}, "filling at 45.0 MiB/s, full in 38m"},
		{Fill{Valid: true, RateBps: 45 * mib, Avail: 0}, "filling at 45.0 MiB/s, full"},
		{Fill{Valid: true, RateBps: 8192, Avail: 1 << 50}, "filling at 8.0 KiB/s"},
		{Fill{Valid: true, RateBps: -mib, Avail: 1}, "freeing at 1.0 MiB/s"},
		{Fill{Valid: true, RateBps: 100, Avail: 1}, "stable"},
	}
	for _, c := range cases {
		if got := c.f.String(); got != c.want {
			t.Errorf("%+v.String() = %q, want %q", c.f, got, c.want)
		}
	}
}

func TestStringsAreCleaned(t *testing.T) {
	w := Writer{PID: 1234, Comm: "evil\x1b]52;c;AAAA\x07", WriteBps: 20 * mib}
	g := GrowingFile{Path: "/tmp/a\nb\x1b[2J", Deleted: true, GrowthBps: 20 * mib, PID: 1234, Comm: "java‮", FD: 3}
	for _, s := range []string{w.String(), g.String()} {
		if strings.ContainsAny(s, "\x1b\x07\n‮") {
			t.Errorf("unsafe characters survived: %q", s)
		}
	}
	if got, want := g.String(), `/tmp/a\nb?[2J (deleted) +20.0 MiB/s — java? pid 1234`; got != want {
		t.Errorf("GrowingFile.String() = %q, want %q", got, want)
	}
	if got, want := (Writer{PID: 7, Comm: "java", WriteBps: 1024}).String(), "java (pid 7) 1.0 KiB/s"; got != want {
		t.Errorf("Writer.String() = %q, want %q", got, want)
	}
}
