package live

import (
	"math"
	"path/filepath"
	"testing"
	"time"
)

const mib = 1 << 20

var t0 = time.Date(2026, 11, 14, 12, 0, 0, 0, time.UTC)

// feed adds n samples every step from start, with Used growing linearly at
// rate bytes/s (plus noise(i)) and Avail shrinking by the same amount.
func feed(m *FillMeter, start time.Time, step time.Duration, n int, used0, avail0 int64, rate float64, noise func(i int) int64) Fill {
	var f Fill
	for i := 0; i < n; i++ {
		el := step * time.Duration(i)
		d := int64(rate * el.Seconds())
		if noise != nil {
			d += noise(i)
		}
		f = m.Add(FillSample{At: start.Add(el), Used: used0 + d, Avail: avail0 - d})
	}
	return f
}

func near(got, want, tol float64) bool {
	return math.Abs(got-want) <= tol
}

func TestFillFilling(t *testing.T) {
	m := NewFillMeter("/", time.Minute)
	const rate = 45 * mib
	avail0 := int64(100 << 30)
	f := feed(m, t0, 2*time.Second, 11, 50<<30, avail0, rate, nil) // spans 20 s
	if !f.Valid {
		t.Fatal("Valid = false after a 20 s span")
	}
	if !near(f.RateBps, rate, 1) {
		t.Errorf("RateBps = %.1f, want %d", f.RateBps, rate)
	}
	wantAvail := avail0 - int64(rate*20)
	if f.Avail != wantAvail || f.Used != 50<<30+int64(rate*20) {
		t.Errorf("Used/Avail = %d/%d, want latest sample", f.Used, f.Avail)
	}
	wantETA := time.Duration(float64(wantAvail) / rate * float64(time.Second))
	if d := f.ETA - wantETA; d < -time.Second || d > time.Second {
		t.Errorf("ETA = %v, want %v", f.ETA, wantETA)
	}
}

func TestFillJitterUsesRegression(t *testing.T) {
	m := NewFillMeter("/", time.Minute)
	const rate = 10 * mib
	// ±8 MiB of alternating noise per sample: an endpoint estimate over
	// 2 s would be off by up to 8 MiB/s, the regression over 60 s is not.
	noise := func(i int) int64 {
		if i%2 == 0 {
			return 8 * mib
		}
		return -8 * mib
	}
	f := feed(m, t0, 2*time.Second, 31, 1<<30, 1<<40, rate, noise)
	if !near(f.RateBps, rate, 0.05*rate) {
		t.Errorf("RateBps = %.0f, want %d ±5%%", f.RateBps, rate)
	}
}

func TestFillFreeing(t *testing.T) {
	m := NewFillMeter("/", 0)
	f := feed(m, t0, time.Second, 10, 80<<30, 20<<30, -10*mib, nil)
	if !f.Valid || !near(f.RateBps, -10*mib, 1) {
		t.Errorf("RateBps = %.1f Valid = %v, want -10 MiB/s valid", f.RateBps, f.Valid)
	}
	if f.ETA != 0 {
		t.Errorf("ETA = %v while freeing, want 0", f.ETA)
	}
}

func TestFillFlat(t *testing.T) {
	m := NewFillMeter("/", 0)
	f := feed(m, t0, 2*time.Second, 5, 1<<30, 1<<30, 0, nil)
	if !f.Valid || f.RateBps != 0 || f.ETA != 0 {
		t.Errorf("flat: %+v, want valid, rate 0, ETA 0", f)
	}
}

func TestFillShortSpanNotValid(t *testing.T) {
	m := NewFillMeter("/", 0)
	f := m.Add(FillSample{At: t0, Used: 100, Avail: 1 << 30})
	if f.Valid || f.RateBps != 0 || f.ETA != 0 || f.Used != 100 || f.Avail != 1<<30 {
		t.Errorf("one sample: %+v", f)
	}
	f = m.Add(FillSample{At: t0.Add(2 * time.Second), Used: 100 + 2*mib, Avail: 1<<30 - 2*mib})
	if f.Valid {
		t.Error("Valid = true with a 2 s span")
	}
	if f.ETA != 0 {
		t.Errorf("ETA = %v before Valid, want 0", f.ETA)
	}
	if !near(f.RateBps, mib, 1) {
		t.Errorf("provisional RateBps = %.1f, want (last-first)/dt = %d", f.RateBps, mib)
	}
	f = m.Add(FillSample{At: t0.Add(4 * time.Second), Used: 100 + 4*mib, Avail: 1<<30 - 4*mib})
	if !f.Valid || f.ETA <= 0 {
		t.Errorf("4 s span: %+v, want Valid with an ETA", f)
	}
}

func TestFillWindowEviction(t *testing.T) {
	m := NewFillMeter("/", 10*time.Second)
	// 20 s at +100 MiB/s, then 10 s at -50 MiB/s: only the last 10 s count.
	var used int64 = 1 << 30
	var f Fill
	for i := 0; i <= 30; i++ {
		if i > 0 {
			if i <= 20 {
				used += 100 * mib
			} else {
				used -= 50 * mib
			}
		}
		f = m.Add(FillSample{At: t0.Add(time.Duration(i) * time.Second), Used: used, Avail: 1<<40 - used})
	}
	if !near(f.RateBps, -50*mib, 1) {
		t.Errorf("RateBps = %.0f, want -50 MiB/s (old samples evicted)", f.RateBps)
	}
	if len(m.samples) != 11 {
		t.Errorf("kept %d samples, want 11 (t=20..30 s)", len(m.samples))
	}

	// A long run must not grow the backing array.
	for i := 31; i < 2000; i++ {
		m.Add(FillSample{At: t0.Add(time.Duration(i) * time.Second), Used: used, Avail: 1 << 30})
	}
	if len(m.samples) != 11 || cap(m.samples) > 64 {
		t.Errorf("after 2000 samples: len %d cap %d", len(m.samples), cap(m.samples))
	}
}

func TestFillETACapAndFull(t *testing.T) {
	m := NewFillMeter("/", 0)
	f := feed(m, t0, 2*time.Second, 5, 1<<30, 1<<40, 1, nil) // 1 B/s, 1 TiB free
	if !f.Valid || f.RateBps <= 0 {
		t.Fatalf("slow fill: %+v", f)
	}
	if f.ETA != 0 {
		t.Errorf("ETA = %v beyond MaxETA, want 0", f.ETA)
	}

	m = NewFillMeter("/", 0)
	for i := 0; i < 5; i++ {
		f = m.Add(FillSample{At: t0.Add(time.Duration(i) * 2 * time.Second), Used: int64(i) * mib, Avail: 0})
	}
	if !f.Valid || f.RateBps <= 0 || f.ETA != 0 {
		t.Errorf("full filesystem: %+v, want filling with ETA 0", f)
	}
}

func TestFillClockSkew(t *testing.T) {
	m := NewFillMeter("/", 0)
	feed(m, t0, time.Second, 10, 0, 1<<30, mib, nil)

	// Same instant as the newest sample: replaces it.
	f := m.Add(FillSample{At: t0.Add(9 * time.Second), Used: 123, Avail: 456})
	if len(m.samples) != 10 || f.Used != 123 || f.Avail != 456 {
		t.Errorf("same-time sample: len %d, %+v", len(m.samples), f)
	}

	// Clock went backwards: history discarded.
	f = m.Add(FillSample{At: t0.Add(5 * time.Second), Used: 1, Avail: 2})
	if len(m.samples) != 1 || f.Valid || f.RateBps != 0 {
		t.Errorf("backwards sample: len %d, %+v", len(m.samples), f)
	}
}

func TestNewFillMeterDefaultWindow(t *testing.T) {
	for _, w := range []time.Duration{0, -time.Second} {
		if m := NewFillMeter("/", w); m.window != DefaultWindow {
			t.Errorf("window(%v) = %v, want %v", w, m.window, DefaultWindow)
		}
	}
}

func TestFillSampleStatfs(t *testing.T) {
	m := NewFillMeter(t.TempDir(), 0)
	f, err := m.Sample(time.Now())
	if err != nil {
		t.Skipf("statfs unavailable: %v", err)
	}
	if f.Used < 0 || f.Avail < 0 || f.Used+f.Avail == 0 {
		t.Errorf("statfs sample: %+v", f)
	}

	bad := NewFillMeter(filepath.Join(t.TempDir(), "missing"), 0)
	if _, err := bad.Sample(time.Now()); err == nil {
		t.Error("Sample on a missing path: want an error")
	}
	if len(bad.samples) != 0 {
		t.Error("a failed Sample must not add a sample")
	}
}

func TestUsage(t *testing.T) {
	if u, a := usage(100, 40, 30, 4096); u != 60*4096 || a != 30*4096 {
		t.Errorf("usage = %d, %d", u, a)
	}
	if u, _ := usage(100, 120, 30, 4096); u != 0 {
		t.Errorf("free > total: used = %d, want 0", u)
	}
	if u, a := usage(100, 40, 30, 0); u != 0 || a != 0 {
		t.Errorf("bsize 0: %d, %d", u, a)
	}
}
