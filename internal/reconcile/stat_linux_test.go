//go:build linux

package reconcile

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/sys/unix"
)

const sampleMountinfo = `22 1 8:1 / / rw,relatime shared:1 - ext4 /dev/sda1 rw,errors=remount-ro
23 22 0:21 / /var rw,relatime shared:2 - xfs /dev/sdb1 rw
24 22 0:22 / /variable rw - ext4 /dev/sdc1 rw
25 23 0:23 / /var/log rw,nosuid master:3 propagate_from:1 - btrfs /dev/sdd1 rw,subvol=/log
26 22 0:24 / /mnt/my\040disk\011x rw - vfat /dev/sde\134p1 rw
27 22 0:25 / /tmp rw - tmpfs  rw
28 22 0:26 / /data rw - ext4 /dev/sdf1 rw
29 28 0:27 / /data rw - zfs tank/data rw
30 22 0:28 / /bad\04 rw - ext4 /dev/x rw
31 22 0:29 / relative rw - ext4 /dev/y rw
garbage line
32 22 0:30 / /proc rw,nosuid - proc none rw
`

func TestParseMountinfo(t *testing.T) {
	ms := parseMountinfo(sampleMountinfo)
	byPoint := map[string]mountEntry{}
	for _, m := range ms {
		byPoint[m.point] = m
	}
	for _, want := range []mountEntry{
		{"/", "/dev/sda1", "ext4"},
		{"/var/log", "/dev/sdd1", "btrfs"}, // two optional fields
		{"/mnt/my disk\tx", `/dev/sde\p1`, "vfat"},
		{"/tmp", "", "tmpfs"}, // empty source keeps the fields in place
		{"/proc", "", "proc"}, // "none" means no source
	} {
		if got, ok := byPoint[want.point]; !ok || got != want {
			t.Errorf("entry %q = %+v (found %v), want %+v", want.point, got, ok, want)
		}
	}
	for _, p := range []string{"/bad\\04", "relative", "/bad"} {
		if _, ok := byPoint[p]; ok {
			t.Errorf("malformed entry %q was accepted", p)
		}
	}
	if len(ms) != 9 {
		t.Errorf("got %d entries, want 9: %+v", len(ms), ms)
	}
}

func TestLongestMount(t *testing.T) {
	ms := parseMountinfo(sampleMountinfo)
	cases := []struct{ path, mount, fstype string }{
		{"/", "/", "ext4"},
		{"/var", "/var", "xfs"},
		{"/var/lib/x", "/var", "xfs"},
		{"/variable/x", "/variable", "ext4"}, // not /var
		{"/varx", "/", "ext4"},
		{"/var/log/nginx/access.log", "/var/log", "btrfs"},
		{"/mnt/my disk\tx/photo", "/mnt/my disk\tx", "vfat"},
		{"/mnt/my disk", "/", "ext4"},
		{"/data/a", "/data", "zfs"}, // stacked mount: the later one is visible
		{"/home/u", "/", "ext4"},
	}
	for _, c := range cases {
		m, ok := longestMount(ms, c.path)
		if !ok || m.point != c.mount || m.fstype != c.fstype {
			t.Errorf("longestMount(%q) = %+v, %v; want %s (%s)", c.path, m, ok, c.mount, c.fstype)
		}
	}
	if _, ok := longestMount(nil, "/x"); ok {
		t.Error("no mounts must not match")
	}
}

func TestUnescapeOctal(t *testing.T) {
	cases := []struct {
		in, want string
		ok       bool
	}{
		{"/plain", "/plain", true},
		{`/a\040b`, "/a b", true},
		{`\134`, `\`, true},
		{`/end\040`, "/end ", true},
		{`/short\04`, "", false},
		{`/bad\0x9`, "", false},
		{`/big\777`, "", false},
	}
	for _, c := range cases {
		got, ok := unescapeOctal(c.in)
		if got != c.want || ok != c.ok {
			t.Errorf("unescapeOctal(%q) = %q, %v; want %q, %v", c.in, got, ok, c.want, c.ok)
		}
	}
}

func devOf(t *testing.T, p string) uint64 {
	t.Helper()
	var st unix.Stat_t
	if err := unix.Stat(p, &st); err != nil {
		t.Fatal(err)
	}
	return uint64(st.Dev)
}

func TestStatWithFakeMountinfo(t *testing.T) {
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	sub := filepath.Join(dir, "a", "b")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	mi := filepath.Join(t.TempDir(), "mountinfo")
	content := "1 0 8:1 / / rw - ext4 /dev/root rw\n" +
		fmt.Sprintf("2 1 8:2 / %s rw shared:7 - xfs /dev/fake rw\n", strings.ReplaceAll(dir, " ", `\040`))
	if err := os.WriteFile(mi, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	info, err := statWith(sub, mi)
	if err != nil {
		t.Fatal(err)
	}
	if info.MountPoint != dir || info.FSType != "xfs" || info.Source != "/dev/fake" {
		t.Errorf("mount = %q (%s, %s), want %q (xfs, /dev/fake)", info.MountPoint, info.FSType, info.Source, dir)
	}
	if want := devOf(t, dir); info.Dev != want {
		t.Errorf("Dev = %d, want %d", info.Dev, want)
	}
	checkSizes(t, info)

	// A relative path and a symlink to the directory resolve the same way.
	link := filepath.Join(t.TempDir(), "link")
	if err := os.Symlink(sub, link); err != nil {
		t.Fatal(err)
	}
	if info2, err := statWith(link, mi); err != nil || info2.MountPoint != dir {
		t.Errorf("via symlink: %+v, %v", info2, err)
	}
}

func TestStatWithoutMountinfo(t *testing.T) {
	dir := t.TempDir()
	info, err := statWith(dir, filepath.Join(dir, "missing"))
	if err != nil {
		t.Fatal(err)
	}
	real, _ := filepath.EvalSymlinks(dir)
	if !pathHasPrefix(real, info.MountPoint) {
		t.Errorf("fallback mount point %q does not contain %q", info.MountPoint, real)
	}
	if info.Dev != devOf(t, real) {
		t.Errorf("fallback Dev = %d, want the directory's %d", info.Dev, devOf(t, real))
	}
	checkSizes(t, info)
}

func TestStatReal(t *testing.T) {
	info, err := Stat("/")
	if err != nil {
		t.Fatal(err)
	}
	if info.MountPoint != "/" || info.Dev != devOf(t, "/") {
		t.Errorf("Stat(/) = %+v", info)
	}
	if _, err := os.Stat(mountinfoPath); err == nil && info.FSType == "" {
		t.Errorf("Stat(/) found no filesystem type: %+v", info)
	}
	checkSizes(t, info)

	if _, err := Stat(filepath.Join(t.TempDir(), "does-not-exist")); err == nil {
		t.Error("Stat of a missing path must fail")
	}
}

func checkSizes(t *testing.T, info FSInfo) {
	t.Helper()
	if info.Total <= 0 || info.Used < 0 || info.Avail < 0 || info.Reserved < 0 ||
		info.Used > info.Total || info.Avail > info.Total || info.Reserved > info.Total {
		t.Errorf("implausible sizes: %+v", info)
	}
}
