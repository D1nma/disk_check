//go:build linux

package scanner

import (
	"runtime"
	"strings"
	"testing"
)

func TestLoadMounts(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("mountinfo is Linux-only")
	}
	set := loadMounts()
	if set == nil {
		t.Skip("no mountinfo available")
	}
	if _, ok := set["/"]; !ok {
		t.Errorf("root must be a mount point, got %d entries", len(set))
	}
	for mp := range set {
		if !strings.HasPrefix(mp, "/") {
			t.Errorf("mount point %q is not absolute", mp)
		}
	}
}

func TestUnescapeMountPath(t *testing.T) {
	cases := []struct{ in, want string }{
		{"/mnt/simple", "/mnt/simple"},
		{"/mnt/with\\040space", "/mnt/with space"},
		{"/mnt/with\\011tab", "/mnt/with\ttab"},
		{"/mnt/with\\134backslash", "/mnt/with\\backslash"},
	}
	for _, c := range cases {
		got, ok := unescapeMountPath(c.in)
		if !ok || got != c.want {
			t.Errorf("unescapeMountPath(%q) = %q, %v; want %q, true", c.in, got, ok, c.want)
		}
	}
	if _, ok := unescapeMountPath("/bad\\4suffix"); ok {
		t.Error("truncated escape must be rejected")
	}
}
