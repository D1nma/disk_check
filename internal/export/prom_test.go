package export

import (
	"bytes"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/D1nma/disk_check/internal/scanner"
)

const mib = 1 << 20

// promTree:
//
//	/var
//	├── big.iso                1 GiB file (files are never listed)
//	├── cache/                 partial: priv/ is unreadable
//	│   ├── data.bin           500 MiB
//	│   └── priv/              unreadable, 0 bytes
//	├── log/
//	│   ├── nginx/             150 MiB + 2 inodes
//	│   │   ├── access.log     30 MiB
//	│   │   └── old/           120 MiB (depth 3)
//	│   ├── journal/           40 MiB (below 100 MiB)
//	│   └── Apache/            110 MiB (sorts before "journal" and "nginx")
//	├── tmp/                   10 MiB
//	└── we"ird\name<LF>x/      200 MiB
func promTree() *scanner.Node {
	root := dir("/var", 4096, 0, 0,
		file("big.iso", 1<<30, 0, 0),
		dir("log", 4096, 0, 0,
			dir("nginx", 4096, 0, 0,
				file("access.log", 30*mib, 0, 0),
				dir("old", 4096, 0, 0, file("x.gz", 120*mib, 0, 0)),
			),
			dir("journal", 4096, 0, 0, file("system.journal", 40*mib, 0, 0)),
			dir("Apache", 4096, 0, 0, file("error.log", 110*mib, 0, 0)),
		),
		dir("cache", 4096, 0, 0,
			file("data.bin", 500*mib, 0, 0),
			dir("priv", 0, 0, scanner.FlagUnreadable),
		),
		dir("tmp", 4096, 0, 0, file("t", 10*mib, 0, 0)),
		dir("we\"ird\\name\nx", 4096, 0, 0, file("f", 200*mib, 0, 0)),
	)
	testAggregate(root)
	return root
}

func writeProm(t *testing.T, root *scanner.Node, opts PromOptions) string {
	t.Helper()
	var buf bytes.Buffer
	if err := WriteProm(&buf, root, opts); err != nil {
		t.Fatalf("WriteProm: %v", err)
	}
	return buf.String()
}

func TestWritePromGolden(t *testing.T) {
	const (
		nginx   = 4096 + 30*mib + 4096 + 120*mib
		journal = 4096 + 40*mib
		apache  = 4096 + 110*mib
		log     = 4096 + nginx + journal + apache
		cache   = 4096 + 500*mib
		tmp     = 4096 + 10*mib
		weird   = 4096 + 200*mib
		root    = 4096 + 1<<30 + log + cache + tmp + weird
	)
	got := writeProm(t, promTree(), PromOptions{
		MaxDepth:  2,
		MinBytes:  100 * mib,
		Labels:    map[string]string{"host": `srv "1"\`, "env": "prod\nB"},
		Timestamp: 1731577200,
	})

	l := `{env="prod\nB",host="srv \"1\"\\",path=`
	series := []struct {
		path                string
		bytes, files, incpl int64
	}{
		{`"/var"`, root, 8, 1},
		{`"/var/cache"`, cache, 1, 1},
		{`"/var/log"`, log, 4, 0},
		{`"/var/log/Apache"`, apache, 1, 0},
		{`"/var/log/nginx"`, nginx, 2, 0},
		{`"/var/we\"ird\\name\nx"`, weird, 1, 0},
	}
	var want strings.Builder
	want.WriteString("# HELP disk_explorer_directory_bytes Disk usage of a directory, du-style (bytes).\n" +
		"# TYPE disk_explorer_directory_bytes gauge\n")
	for _, s := range series {
		fmt.Fprintf(&want, "disk_explorer_directory_bytes%s%s} %d\n", l, s.path, s.bytes)
	}
	want.WriteString("# HELP disk_explorer_directory_files Number of files below a directory.\n" +
		"# TYPE disk_explorer_directory_files gauge\n")
	for _, s := range series {
		fmt.Fprintf(&want, "disk_explorer_directory_files%s%s} %d\n", l, s.path, s.files)
	}
	want.WriteString("# HELP disk_explorer_directory_incomplete 1 if part of the directory could not be read.\n" +
		"# TYPE disk_explorer_directory_incomplete gauge\n")
	for _, s := range series {
		fmt.Fprintf(&want, "disk_explorer_directory_incomplete%s%s} %d\n", l, s.path, s.incpl)
	}
	want.WriteString("# HELP disk_explorer_scan_timestamp_seconds Unix time when the scan finished.\n" +
		"# TYPE disk_explorer_scan_timestamp_seconds gauge\n" +
		`disk_explorer_scan_timestamp_seconds{env="prod\nB",host="srv \"1\"\\"} 1731577200` + "\n")

	if got != want.String() {
		t.Errorf("output mismatch\n--- got\n%s\n--- want\n%s", got, want.String())
	}
}

// promPaths returns the path labels of the bytes metric, in output order.
func promPaths(out string) []string {
	var paths []string
	for _, line := range strings.Split(out, "\n") {
		rest, ok := strings.CutPrefix(line, MetricDirectoryBytes+"{")
		if !ok {
			continue
		}
		i := strings.Index(rest, `path="`)
		j := strings.LastIndex(rest, `"}`)
		paths = append(paths, rest[i+len(`path="`):j])
	}
	return paths
}

func TestWritePromFiltering(t *testing.T) {
	cases := []struct {
		name string
		opts PromOptions
		want string
	}{
		{"defaults", PromOptions{},
			`/var /var/cache /var/log /var/log/Apache /var/log/nginx /var/we\"ird\\name\nx`},
		{"depth 1", PromOptions{MaxDepth: 1},
			`/var /var/cache /var/log /var/we\"ird\\name\nx`},
		{"depth 3", PromOptions{MaxDepth: 3},
			`/var /var/cache /var/log /var/log/Apache /var/log/nginx /var/log/nginx/old /var/we\"ird\\name\nx`},
		{"no minimum", PromOptions{MinBytes: -1},
			`/var /var/cache /var/cache/priv /var/log /var/log/Apache /var/log/journal /var/log/nginx /var/tmp /var/we\"ird\\name\nx`},
		{"high minimum", PromOptions{MinBytes: 400 * mib},
			`/var /var/cache`},
		{"root below minimum", PromOptions{MinBytes: 1 << 40},
			`/var`},
	}
	for _, c := range cases {
		out := writeProm(t, promTree(), c.opts)
		if got := strings.Join(promPaths(out), " "); got != c.want {
			t.Errorf("%s:\n got %s\nwant %s", c.name, got, c.want)
		}
		if !strings.Contains(out, "{path") {
			t.Errorf("%s: no extra labels expected: %s", c.name, out)
		}
	}
}

func TestWritePromSubtreeRoot(t *testing.T) {
	logDir := promTree().Children[1]
	out := writeProm(t, logDir, PromOptions{MaxDepth: 1, MinBytes: -1})
	if got := strings.Join(promPaths(out), " "); got != "/var/log /var/log/Apache /var/log/journal /var/log/nginx" {
		t.Errorf("paths = %s", got)
	}
}

func TestWritePromLabels(t *testing.T) {
	for _, bad := range []string{"path", "__name__", "__x", "", "1abc", "a-b", "é", "a b"} {
		var buf bytes.Buffer
		err := WriteProm(&buf, promTree(), PromOptions{Labels: map[string]string{"host": "h", bad: "v"}})
		if err == nil {
			t.Errorf("label %q accepted", bad)
		}
		if buf.Len() != 0 {
			t.Errorf("label %q: output written despite the error", bad)
		}
	}
	for _, good := range []string{"_x", "a1", "Zone_2", "x"} {
		if err := WriteProm(&bytes.Buffer{}, promTree(), PromOptions{Labels: map[string]string{good: "v"}}); err != nil {
			t.Errorf("label %q rejected: %v", good, err)
		}
	}
}

func TestWritePromTimestampDefault(t *testing.T) {
	before := time.Now().Unix()
	out := writeProm(t, promTree(), PromOptions{})
	after := time.Now().Unix()
	var ts int64
	for _, line := range strings.Split(out, "\n") {
		if v, ok := strings.CutPrefix(line, MetricScanTimestamp+" "); ok {
			fmt.Sscan(v, &ts)
		}
	}
	if ts < before || ts > after {
		t.Errorf("timestamp %d not in [%d, %d]:\n%s", ts, before, after, out)
	}
}

func TestWritePromInvalidUTF8(t *testing.T) {
	root := dir("/r", 0, 0, 0,
		dir("\xff", 0, 0, 0, file("a", 200*mib, 0, 0)),
		dir("\xfe", 0, 0, 0, file("b", 300*mib, 0, 0)),
		dir("ok\xffé", 0, 0, 0, file("c", 300*mib, 0, 0)),
	)
	testAggregate(root)
	out := writeProm(t, root, PromOptions{})
	// "\xfe" sorts first; "\xff" maps to the same label and is dropped.
	if got := strings.Join(promPaths(out), " "); got != "/r /r/ok\ufffdé /r/\ufffd" {
		t.Errorf("paths = %q", got)
	}
	if !strings.Contains(out, `{path="/r/`+"\ufffd"+`"} 314572800`) {
		t.Errorf("first colliding directory not kept:\n%s", out)
	}
}

func TestWritePromFileRootAndNil(t *testing.T) {
	out := writeProm(t, file("/etc/passwd", 4096, 0, 0), PromOptions{})
	if !strings.Contains(out, `disk_explorer_directory_bytes{path="/etc/passwd"} 4096`) {
		t.Errorf("file root:\n%s", out)
	}
	if err := WriteProm(&bytes.Buffer{}, nil, PromOptions{}); err == nil {
		t.Error("nil root accepted")
	}
}
