package remote

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestParseTarget(t *testing.T) {
	ok := map[string]Target{
		"web1":                {Host: "web1", Path: "/"},
		"root@10.0.0.5:/var/": {Host: "root@10.0.0.5", Path: "/var"},
		"db_1.example.com:/":  {Host: "db_1.example.com", Path: "/"},
	}
	for in, want := range ok {
		got, err := ParseTarget(in)
		if err != nil || got != want {
			t.Errorf("ParseTarget(%q) = %+v, %v; want %+v", in, got, err, want)
		}
	}
	for _, bad := range []string{"-oProxyCommand=x", "host;rm", "host:relative", "host:/a'b", "host:/a$(x)", "host:/a\nb", "", "a b"} {
		if _, err := ParseTarget(bad); err == nil {
			t.Errorf("ParseTarget(%q) accepted", bad)
		}
	}
}

func TestPlatformFromUname(t *testing.T) {
	cases := map[string]string{
		"Linux x86_64": "linux/amd64", "Linux aarch64": "linux/arm64", "Linux armv7l": "linux/arm",
		"Linux armv6l": "linux/arm", "Linux i686": "linux/386", "Linux mips": "linux/mips",
		"Linux mips64el": "linux/mips64le", "Darwin arm64": "darwin/arm64", "Linux riscv64": "linux/riscv64",
	}
	for in, want := range cases {
		os, arch, err := platformFromUname(in)
		if err != nil || os+"/"+arch != want {
			t.Errorf("%q → %s/%s %v, want %s", in, os, arch, err, want)
		}
	}
	for _, bad := range []string{"FreeBSD amd64", "Linux sparc64", "garbage"} {
		if _, _, err := platformFromUname(bad); err == nil {
			t.Errorf("%q accepted", bad)
		}
	}
}

func TestRemoteCommandIsLoginShellSafe(t *testing.T) {
	cmd := remoteCommand(Target{Host: "h", Path: "/srv/data dir"}, Options{Sudo: true, Mode: "partition", Excludes: []string{"/srv/cache"}})
	if !strings.HasPrefix(cmd, "sh -c '") || !strings.HasSuffix(cmd, "'") {
		t.Fatalf("unexpected wrapper: %s", cmd)
	}
	inner := cmd[len("sh -c '") : len(cmd)-1]
	if strings.ContainsAny(inner, `'\`) {
		t.Fatalf("the script must contain no single quote or backslash:\n%s", inner)
	}
	for _, want := range []string{`sudo -n "$d/de"`, `"--mode" "partition"`, `"--exclude" "/srv/cache"`, `"--" "/srv/data dir"`} {
		if !strings.Contains(inner, want) {
			t.Errorf("missing %s in:\n%s", want, inner)
		}
	}
}

// fakeBinary emulates a remote disk-explorer: --version works, --export -
// prints a small ncdu document and exits with the given code.
func fakeBinary(t *testing.T, code string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "de")
	script := `#!/bin/sh
case "$1" in --version) echo "disk-explorer test"; exit 0 ;; esac
cat <<'JSON'
[1,2,{"progname":"disk-explorer","timestamp":1700000000,"de_host":"remote-box","de_fs_total":1000,"de_fs_used":400,"de_fs_avail":500},
[{"name":"/srv","dsize":4096},{"name":"big","dsize":8192},[{"name":"logs","dsize":4096},{"name":"app.log","dsize":16384}]]]
JSON
echo "warning from remote" >&2
exit ` + code + "\n"
	if err := os.WriteFile(p, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return p
}

func withFakeSSH(t *testing.T) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("POSIX sh needed")
	}
	dir := t.TempDir()
	src, _ := filepath.Abs("testdata/fake-ssh")
	if err := os.Symlink(src, filepath.Join(dir, "ssh")); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
}

func TestScanThroughFakeSSH(t *testing.T) {
	withFakeSSH(t)
	var msgs []string
	r, err := Scan(context.Background(), Target{Host: "box", Path: "/srv"}, Options{
		Binary: fakeBinary(t, "2"), Status: func(s string) { msgs = append(msgs, s) },
	})
	if err != nil {
		t.Fatal(err)
	}
	if !r.Partial || r.Root == nil || r.Root.Size != 4096+8192+4096+16384 {
		t.Fatalf("unexpected result: partial=%v root=%+v", r.Partial, r.Root)
	}
	if r.Meta.Host != "remote-box" || r.Meta.FSTotal != 1000 {
		t.Errorf("meta not read: %+v", r.Meta)
	}
	if r.Platform != runtime.GOOS+"/"+runtime.GOARCH && !strings.Contains(r.Platform, "/") {
		t.Errorf("platform = %q", r.Platform)
	}
	if len(msgs) == 0 {
		t.Error("no status message")
	}
}

func TestScanErrors(t *testing.T) {
	withFakeSSH(t)
	_, err := Scan(context.Background(), Target{Host: "badhost", Path: "/"}, Options{Binary: fakeBinary(t, "0")})
	if err == nil || !strings.Contains(err.Error(), "connexion SSH impossible") {
		t.Fatalf("unreachable host: %v", err)
	}
	_, err = Scan(context.Background(), Target{Host: "box", Path: "/"}, Options{Binary: fakeBinary(t, "1")})
	if err == nil || !strings.Contains(err.Error(), "échec du scan distant") {
		t.Fatalf("remote failure: %v", err)
	}
}

func TestScanLeavesNoTempDir(t *testing.T) {
	withFakeSSH(t)
	tmp := t.TempDir()
	t.Setenv("TMPDIR", tmp)
	if _, err := Scan(context.Background(), Target{Host: "box", Path: "/srv"}, Options{Binary: fakeBinary(t, "0")}); err != nil {
		t.Fatal(err)
	}
	left, _ := filepath.Glob(filepath.Join(tmp, "disk-explorer.*"))
	if len(left) != 0 {
		t.Fatalf("remote temporary directory left behind: %v", left)
	}
}
