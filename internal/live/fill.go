// Package live measures what is happening on a filesystem right now, for
// the "disk is filling up" incident view:
//
//   - FillMeter samples statfs and derives the fill rate and the time left
//     until the filesystem is full ("filling at 45 MiB/s, full in 38m").
//   - Tracker polls /proc and reports which processes are writing and which
//     open files are growing ("app.log +20 MiB/s, java pid 1234").
//
// Both are meant to be polled every couple of seconds by the TUI. Neither is
// safe for concurrent use: call each from one goroutine at a time, off the UI
// goroutine (Poll and Sample make blocking system calls).
//
// Strings in the returned structures (process names, paths) are raw and
// attacker-controlled. Pass them through textsafe.Clean before printing, or
// use the String methods and Format helpers of this package, which do.
package live

import (
	"time"
)

const (
	// DefaultWindow is the FillMeter window used when NewFillMeter is given
	// a non-positive one.
	DefaultWindow = 60 * time.Second

	// MinValidSpan is how much history a FillMeter needs before its rate is
	// trusted (Fill.Valid) and an ETA is computed.
	MinValidSpan = 4 * time.Second

	// MaxETA caps the reported time to full: beyond it the ETA is reported
	// as 0 (not meaningful for an incident view, and dominated by noise).
	MaxETA = 30 * 24 * time.Hour
)

// FillSample is one statfs reading.
type FillSample struct {
	At    time.Time
	Used  int64 // bytes in use: (blocks - free) * block size
	Avail int64 // bytes available to unprivileged users: bavail * block size
}

// Fill is the state of a filesystem derived from the samples in the window.
type Fill struct {
	Used, Avail int64 // from the most recent sample

	// RateBps is the growth of Used in bytes per second: > 0 filling,
	// < 0 freeing. It is the least-squares slope over the window, computed
	// as soon as two samples exist; treat it as provisional until Valid.
	RateBps float64

	// ETA is the time until Avail reaches 0 at RateBps. It is 0 when the
	// filesystem is not filling, when the estimate is not Valid yet, when
	// Avail is already 0 (full now) or when it would exceed MaxETA.
	ETA time.Duration

	// Valid is true once the samples in the window span at least
	// MinValidSpan.
	Valid bool
}

// FillMeter keeps the statfs samples of one filesystem within a sliding
// time window and derives the fill rate from them.
type FillMeter struct {
	path    string
	window  time.Duration
	samples []FillSample // oldest first, all within window of the newest
}

// NewFillMeter returns a meter for the filesystem holding path. A window
// <= 0 selects DefaultWindow.
func NewFillMeter(path string, window time.Duration) *FillMeter {
	if window <= 0 {
		window = DefaultWindow
	}
	return &FillMeter{path: path, window: window}
}

// Sample reads statfs(path) and adds the reading taken at now. On a statfs
// error the meter is left unchanged and a zero Fill is returned.
func (m *FillMeter) Sample(now time.Time) (Fill, error) {
	used, avail, err := statfsUsage(m.path)
	if err != nil {
		return Fill{}, err
	}
	return m.Add(FillSample{At: now, Used: used, Avail: avail}), nil
}

// Add pushes s, drops the samples older than the window and returns the
// resulting Fill. It does no I/O.
//
// A sample taken at the same instant as the newest one replaces it. A
// sample older than the newest one (the clock went backwards) discards the
// history and starts over from s.
func (m *FillMeter) Add(s FillSample) Fill {
	if n := len(m.samples); n > 0 {
		last := m.samples[n-1].At
		switch {
		case s.At.Equal(last):
			m.samples[n-1] = s
		case s.At.Before(last):
			m.samples = append(m.samples[:0], s)
		default:
			m.samples = append(m.samples, s)
		}
	} else {
		m.samples = append(m.samples, s)
	}

	// Evict samples that fell out of the window. Compact in place so the
	// backing array does not creep forward and grow without bound.
	drop := 0
	for drop < len(m.samples)-1 && s.At.Sub(m.samples[drop].At) > m.window {
		drop++
	}
	if drop > 0 {
		n := copy(m.samples, m.samples[drop:])
		clear(m.samples[n:])
		m.samples = m.samples[:n]
	}

	return computeFill(m.samples)
}

// computeFill derives a Fill from samples ordered oldest first, with
// strictly increasing times.
func computeFill(samples []FillSample) Fill {
	if len(samples) == 0 {
		return Fill{}
	}
	last := samples[len(samples)-1]
	f := Fill{Used: last.Used, Avail: last.Avail}
	if len(samples) < 2 {
		return f
	}
	f.RateBps = slope(samples)
	f.Valid = last.At.Sub(samples[0].At) >= MinValidSpan
	if f.Valid && f.RateBps > 0 && f.Avail > 0 {
		secs := float64(f.Avail) / f.RateBps
		if secs <= MaxETA.Seconds() {
			f.ETA = time.Duration(secs * float64(time.Second))
		}
	}
	return f
}

// slope is the least-squares slope of Used over time, in bytes per second.
// With two samples it is exactly (last-first)/dt. Times and sizes are taken
// relative to the first sample and centred on their means, which keeps the
// float64 arithmetic exact enough for multi-terabyte filesystems.
func slope(samples []FillSample) float64 {
	t0, u0 := samples[0].At, samples[0].Used
	n := float64(len(samples))
	var sx, sy float64
	for _, s := range samples {
		sx += s.At.Sub(t0).Seconds()
		sy += float64(s.Used - u0)
	}
	mx, my := sx/n, sy/n
	var sxx, sxy float64
	for _, s := range samples {
		dx := s.At.Sub(t0).Seconds() - mx
		dy := float64(s.Used-u0) - my
		sxx += dx * dx
		sxy += dx * dy
	}
	if sxx == 0 {
		return 0
	}
	return sxy / sxx
}
