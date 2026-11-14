package export

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"testing/iotest"
	"time"

	"github.com/D1nma/disk_check/internal/scanner"
)

const (
	allFlags      = 0xff
	noHardlinkBit = allFlags &^ scanner.FlagHardlink
)

// readerCase reads a document in one of several ways that exercise
// different paths of ReadNcdu.
type readerCase struct {
	name   string
	wrap   func([]byte) io.Reader
	tokens bool // token path only (no peeking at buffered bytes)
}

func (rc readerCase) read(doc []byte) (*scanner.Node, Meta, error) {
	return readNcdu(rc.wrap(doc), rc.tokens)
}

// readers: whole input; one byte per Read (the decoder's buffer then always
// ends before the next entry, so files take the token fallback); half
// reads; data returned together with io.EOF; and the token path forced for
// every information object.
var readers = []readerCase{
	{"plain", func(b []byte) io.Reader { return bytes.NewReader(b) }, false},
	{"onebyte", func(b []byte) io.Reader { return iotest.OneByteReader(bytes.NewReader(b)) }, false},
	{"half", func(b []byte) io.Reader { return iotest.HalfReader(bytes.NewReader(b)) }, false},
	{"dataerr", func(b []byte) io.Reader { return iotest.DataErrReader(bytes.NewReader(b)) }, false},
	{"tokens", func(b []byte) io.Reader { return bytes.NewReader(b) }, true},
}

// roundTripTree has nested dirs, an unreadable dir, a hard link pair, mount
// roots, Unicode and names with quotes, backslash, newline and controls.
func roundTripTree() *scanner.Node {
	root := dir("/srv/données", 4096, 1700000000, 0,
		file("a.txt", 8192, 1700000001, 0),
		file("we\"ird\\name\nwith\x01ctl\x7fdel\ttab\rcr\x1f", 4096, 0, 0),
		dir("héllo 日本語 🎉", 4096, 1700000002, 0,
			file("link1", 4096, 0, scanner.FlagHardlink),
			dir("sub", 4096, 0, 0,
				file("link2", 0, 0, scanner.FlagHardlink),
			),
		),
		dir("locked", 4096, 0, scanner.FlagUnreadable,
			file("partial.bin", 12288, 0, 0),
		),
		dir("mnt", 4096, 0, scanner.FlagMountRoot,
			file("x", 4096, 0, 0),
			dir("nested-mnt", 0, 0, scanner.FlagMountRoot),
		),
		dir("empty", 0, 0, 0),
	)
	testAggregate(root)
	return root
}

func writeDoc(t testing.TB, root *scanner.Node, meta Meta) []byte {
	t.Helper()
	var buf bytes.Buffer
	if err := WriteNcdu(&buf, root, meta); err != nil {
		t.Fatalf("WriteNcdu: %v", err)
	}
	return buf.Bytes()
}

func TestNcduRoundTrip(t *testing.T) {
	want := roundTripTree()
	if want.Size != 53248 || want.FileCount != 6 || want.DirCount != 6 || want.Flags != scanner.FlagPartial {
		t.Fatalf("fixture: size=%d files=%d dirs=%d flags=%b", want.Size, want.FileCount, want.DirCount, want.Flags)
	}
	meta := Meta{Progname: "disk-explorer", Progver: "v1.2.3", Timestamp: 1731577200}
	doc := writeDoc(t, want, meta)

	if !json.Valid(doc) {
		t.Fatalf("output is not valid JSON:\n%s", doc)
	}
	text := string(doc)
	if !strings.HasPrefix(text, `[1,2,{"progname":"disk-explorer","progver":"v1.2.3","timestamp":1731577200},`) {
		t.Errorf("unexpected header: %.120s", text)
	}
	for _, s := range []string{`"dev":1`, `"dev":2`, `"read_error":true`, `\"ird\\name\nwith\u0001ctl\u007fdel\ttab\rcr\u001f"`} {
		if !strings.Contains(text, s) {
			t.Errorf("output lacks %s", s)
		}
	}
	if strings.Contains(text, "hlnkc") {
		t.Error("output must not contain hlnkc")
	}

	for _, r := range readers {
		t.Run(r.name, func(t *testing.T) {
			got, gotMeta, err := r.read(doc)
			if err != nil {
				t.Fatalf("ReadNcdu: %v", err)
			}
			if gotMeta != meta {
				t.Errorf("meta = %+v, want %+v", gotMeta, meta)
			}
			if got.Parent != nil {
				t.Error("root has a parent")
			}
			// FlagHardlink is deliberately not exported (see package doc).
			compareTrees(t, want, got, noHardlinkBit)
		})
	}
}

func TestNcduWriteDetails(t *testing.T) {
	root := dir("/r", 4096, -5, scanner.FlagMountRoot,
		file("bad\xffname\xc3", 4096, 0, 0),
		dir("d", 0, 0, 0),
	)
	testAggregate(root)
	doc := writeDoc(t, root, Meta{})
	if !json.Valid(doc) {
		t.Fatalf("invalid JSON: %s", doc)
	}
	text := string(doc)
	for _, s := range []string{`"progname":"disk-explorer"`, `"name":"bad` + "\ufffd" + `name` + "\ufffd" + `"`, `{"name":"d"}`} {
		if !strings.Contains(text, s) {
			t.Errorf("output lacks %s:\n%s", s, text)
		}
	}
	for _, s := range []string{`"progver"`, `"mtime"`, `"dev"`} {
		if strings.Contains(text, s) {
			t.Errorf("output must not contain %s:\n%s", s, text)
		}
	}
	got, meta, err := ReadNcdu(bytes.NewReader(doc))
	if err != nil {
		t.Fatal(err)
	}
	if meta.Timestamp == 0 || time.Since(time.Unix(meta.Timestamp, 0)) > time.Minute {
		t.Errorf("default timestamp = %d, want now", meta.Timestamp)
	}
	if got.Children[0].Name != "bad\ufffdname\ufffd" {
		t.Errorf("name = %q", got.Children[0].Name)
	}
	if got.ModTime != 0 || got.Flags != 0 {
		t.Errorf("root mtime=%d flags=%b, want 0 0", got.ModTime, got.Flags)
	}
}

func TestNcduFileRoot(t *testing.T) {
	root := file("/tmp/one.bin", 12288, 1700000000, 0)
	doc := writeDoc(t, root, Meta{Timestamp: 1})
	if want := `{"name":"/tmp/one.bin","asize":12288,"dsize":12288,"mtime":1700000000}]` + "\n"; !strings.HasSuffix(string(doc), want) {
		t.Fatalf("doc = %s", doc)
	}
	got, _, err := ReadNcdu(bytes.NewReader(doc))
	if err != nil {
		t.Fatal(err)
	}
	compareTrees(t, root, got, allFlags)
}

func TestNcduWriteErrors(t *testing.T) {
	if err := WriteNcdu(io.Discard, nil, Meta{}); err == nil {
		t.Error("nil root: no error")
	}
	errBoom := errors.New("boom")
	if err := WriteNcdu(failWriter{errBoom}, roundTripTree(), Meta{}); !errors.Is(err, errBoom) {
		t.Errorf("failing writer: err = %v", err)
	}
}

type failWriter struct{ err error }

func (f failWriter) Write([]byte) (int, error) { return 0, f.err }

func TestAppendJSONString(t *testing.T) {
	var all strings.Builder
	for c := 0; c < 0x80; c++ {
		all.WriteByte(byte(c))
	}
	for _, s := range []string{"", "plain", all.String(), "é日本🎉", "\u2028\u2029", "\xff", "a\xe2\x82b", "\xed\xa0\x80"} {
		out := appendJSONString(nil, s)
		if !json.Valid(out) {
			t.Errorf("%q: invalid JSON %s", s, out)
			continue
		}
		// encoding/json also replaces each invalid byte by U+FFFD.
		ref, _ := json.Marshal(s)
		var got, want string
		_ = json.Unmarshal(out, &got)
		_ = json.Unmarshal(ref, &want)
		if got != want {
			t.Errorf("%q: decodes to %q, want %q", s, got, want)
		}
	}
}

func FuzzAppendJSONString(f *testing.F) {
	for _, s := range []string{"", "a\"b\\c\n", "\x00\x1f\x7f", "é\xff日"} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, s string) {
		out := appendJSONString(nil, s)
		ref, _ := json.Marshal(s)
		var got, want string
		if err := json.Unmarshal(out, &got); err != nil {
			t.Fatalf("%q: %v (%s)", s, err, out)
		}
		_ = json.Unmarshal(ref, &want)
		if got != want {
			t.Fatalf("%q: decodes to %q, want %q", s, got, want)
		}
	})
}

// ncduSample is shaped like ncdu's own output: hard links (hlnkc/nlink,
// including the same inode number on another device), read errors on a
// directory and on a file (failed lstat), excluded entries (a pattern, a
// kernfs directory with content, another filesystem), a non-regular file,
// extended info, an escaped name and an unknown nested field.
const ncduSample = `[1,2,{"progname":"ncdu","progver":"1.19","timestamp":1700000000},
[{"name":"/data","asize":4096,"dsize":4096,"dev":2049,"ino":2,"mtime":1690000000},
{"name":"big.iso","asize":700000000,"dsize":700002304,"ino":12,"mtime":1690000001},
{"name":"hl-a","asize":10,"dsize":4096,"ino":50,"hlnkc":true,"nlink":2},
[{"name":"docs","asize":4096,"dsize":4096,"ino":3},
{"name":"hl-b","asize":10,"dsize":4096,"ino":50,"hlnkc":true,"nlink":2},
{"name":"link","asize":12,"ino":51,"notreg":true},
{"name":"caf\u00e9 \"x\"","asize":1,"dsize":4096,"ino":52,"uid":1000,"gid":1000,"mode":33188}],
[{"name":"secret","asize":4096,"dsize":4096,"ino":4,"read_error":true}],
{"name":"excluded.log","excluded":"pattern"},
[{"name":"proc","excluded":"kernfs"},{"name":"ignored","dsize":999},[{"name":"deeper"}]],
{"name":"other","excluded":"otherfs"},
{"name":"gone","read_error":true},
[{"name":"usb","asize":4096,"dsize":4096,"dev":2065,"ino":2},
{"name":"photo.jpg","asize":8000,"dsize":8192,"ino":50,"hlnkc":true,"nlink":2},
{"name":"photo-copy.jpg","asize":8000,"dsize":8192,"ino":50,"nlink":2}],
{"name":"future","dsize":4096,"ino":60,"xattrs":{"user.a":[1,{"b":null}]},"excluded":null}
]]
`

func TestReadNcduSample(t *testing.T) {
	const (
		hl = scanner.FlagHardlink
		ur = scanner.FlagUnreadable
	)
	want := map[string]summary{
		"/data":                    {true, 700039168, 4096, 9, 3, scanner.FlagPartial, true},
		"/data/big.iso":            {false, 700002304, 0, 1, 0, 0, true},
		"/data/hl-a":               {false, 4096, 0, 1, 0, hl, true},
		"/data/docs":               {true, 8192, 4096, 3, 0, 0, true},
		"/data/docs/hl-b":          {false, 0, 0, 1, 0, hl, true},
		"/data/docs/link":          {false, 0, 0, 1, 0, 0, true},
		`/data/docs/café "x"`:      {false, 4096, 0, 1, 0, 0, true},
		"/data/secret":             {true, 4096, 4096, 0, 0, ur, true},
		"/data/gone":               {false, 0, 0, 1, 0, ur, true},
		"/data/usb":                {true, 12288, 4096, 2, 0, scanner.FlagMountRoot, true},
		"/data/usb/photo.jpg":      {false, 8192, 0, 1, 0, hl, true},
		"/data/usb/photo-copy.jpg": {false, 0, 0, 1, 0, hl, true},
		"/data/future":             {false, 4096, 0, 1, 0, 0, true},
	}
	for _, r := range readers {
		t.Run(r.name, func(t *testing.T) {
			root, meta, err := r.read([]byte(ncduSample))
			if err != nil {
				t.Fatalf("ReadNcdu: %v", err)
			}
			if meta != (Meta{Progname: "ncdu", Progver: "1.19", Timestamp: 1700000000}) {
				t.Errorf("meta = %+v", meta)
			}
			got := summarize(root, allFlags)
			for path, w := range want {
				if g, ok := got[path]; !ok {
					t.Errorf("%s: missing", path)
				} else if g != w {
					t.Errorf("%s:\n got %+v\nwant %+v", path, g, w)
				}
			}
			for path := range got {
				if _, ok := want[path]; !ok {
					t.Errorf("%s: unexpected entry", path)
				}
			}
			if root.ModTime != 1690000000 || root.Children[0].ModTime != 1690000001 {
				t.Errorf("mtime not read: %d %d", root.ModTime, root.Children[0].ModTime)
			}
			// Document order is kept.
			var names []string
			for _, c := range root.Children {
				names = append(names, c.Name)
			}
			if s := strings.Join(names, ","); s != "big.iso,hl-a,docs,secret,gone,usb,future" {
				t.Errorf("children order = %s", s)
			}
		})
	}
}

func TestReadNcduLenient(t *testing.T) {
	cases := map[string]string{
		"minor 0":             `[1,0,{},[{"name":"/"}]]`,
		"minor 99":            `[1,99,{"progname":"x"},[{"name":"/"}]]`,
		"float versions":      `[1.0,2e0,{},[{"name":"/"}]]`,
		"odd meta types":      `[1,2,{"progname":5,"progver":["a"],"timestamp":"x"},[{"name":"/"}]]`,
		"null meta":           `[1,2,null,[{"name":"/"}]]`,
		"extra elements":      `[1,2,{},[{"name":"/"}],{"future":[1,{"a":2}]},7]`,
		"byte order mark":     "\xef\xbb\xbf" + `[1,2,{},[{"name":"/"}]]`,
		"whitespace":          " \n\t[ 1 , 2 , { } ,\n\n  [ { \"name\" : \"/\" } ,\n  { \"name\" : \"f\" } ] ]\n\n",
		"raw invalid utf8":    "[1,2,{},[{\"name\":\"/\"},{\"name\":\"a\xffb\"}]]",
		"null fields":         `[1,2,{},[{"name":"/","dsize":null,"read_error":null},{"name":"f","mtime":null,"hlnkc":null}]]`,
		"case-insensitive":    `[1,2,{},[{"NAME":"/","DSize":4096}]]`,
		"duplicate keys":      `[1,2,{},[{"name":"x","name":"/"}]]`,
		"empty excluded dir":  `[1,2,{},[{"name":"/"},[{"name":"e","excluded":"frmlnk"}]]]`,
		"false/empty exclude": `[1,2,{},[{"name":"/"},{"name":"a","excluded":false},{"name":"b","excluded":""}]]`,
	}
	for name, doc := range cases {
		for _, r := range readers {
			if _, _, err := r.read([]byte(doc)); err != nil {
				t.Errorf("%s/%s: %v", name, r.name, err)
			}
		}
	}

	root, _, err := ReadNcdu(strings.NewReader(cases["raw invalid utf8"]))
	if err != nil {
		t.Fatal(err)
	}
	if n := root.Children[0].Name; n != "a\ufffdb" {
		t.Errorf("raw invalid UTF-8 decoded as %q", n)
	}
	root, _, _ = ReadNcdu(strings.NewReader(cases["case-insensitive"]))
	if root.Size != 4096 {
		t.Errorf("case-insensitive keys: size %d", root.Size)
	}
	root, _, _ = ReadNcdu(strings.NewReader(cases["false/empty exclude"]))
	if len(root.Children) != 2 {
		t.Errorf("false/empty excluded: %d children, want 2", len(root.Children))
	}
}

func TestReadNcduErrors(t *testing.T) {
	cases := []struct {
		name, doc string
		want      error
	}{
		{"empty", "", ErrTruncated},
		{"blank", "  \n", ErrTruncated},
		{"major 2", `[2,0,{},[{"name":"/"}]]`, ErrUnsupportedVersion},
		{"major 0", `[0,1,{},[{"name":"/"}]]`, ErrUnsupportedVersion},
		{"garbage", "hello world", ErrFormat},
		{"object", `{"name":"/"}`, ErrFormat},
		{"only major", `[1]`, ErrFormat},
		{"string major", `["1",2,{},[{"name":"/"}]]`, ErrFormat},
		{"fractional major", `[1.5,2,{},[{"name":"/"}]]`, ErrFormat},
		{"no meta", `[1,2]`, ErrFormat},
		{"no root", `[1,2,{}]`, ErrFormat},
		{"scalar root", `[1,2,{},42]`, ErrFormat},
		{"dir without info", `[1,2,{},[]]`, ErrFormat},
		{"dir starting with array", `[1,2,{},[[{"name":"x"}]]]`, ErrFormat},
		{"missing name", `[1,2,{},[{"dsize":1}]]`, ErrFormat},
		{"empty name", `[1,2,{},[{"name":"/"},{"name":""}]]`, ErrFormat},
		{"slash in name", `[1,2,{},[{"name":"/"},{"name":"a/b"}]]`, ErrFormat},
		{"dotdot", `[1,2,{},[{"name":"/"},[{"name":".."}]]]`, ErrFormat},
		{"dot", `[1,2,{},[{"name":"/"},{"name":"."}]]`, ErrFormat},
		{"nul in name", `[1,2,{},[{"name":"/"},{"name":"a\u0000b"}]]`, ErrFormat},
		{"negative size", `[1,2,{},[{"name":"/"},{"name":"x","dsize":-1}]]`, ErrFormat},
		{"string size", `[1,2,{},[{"name":"/"},{"name":"x","dsize":"12"}]]`, ErrFormat},
		{"float size", `[1,2,{},[{"name":"/"},{"name":"x","dsize":1.5}]]`, ErrFormat},
		{"size overflow", `[1,2,{},[{"name":"/"},{"name":"x","dsize":99999999999999999999}]]`, ErrFormat},
		{"number name", `[1,2,{},[{"name":"/"},{"name":7}]]`, ErrFormat},
		{"object name", `[1,2,{},[{"name":"/"},{"name":{"a":1}}]]`, ErrFormat},
		{"bool as string", `[1,2,{},[{"name":"/"},{"name":"x","hlnkc":"yes"}]]`, ErrFormat},
		{"scalar entry", `[1,2,{},[{"name":"/"},5]]`, ErrFormat},
		{"scalar in subdir", `[1,2,{},[{"name":"/"},[{"name":"d"},"x"]]]`, ErrFormat},
		{"trailing comma", `[1,2,{},[{"name":"/"},]]`, ErrFormat},
		{"trailing data", `[1,2,{},[{"name":"/"}]] x`, ErrFormat},
		{"second document", `[1,2,{},[{"name":"/"}]][1,2,{},[{"name":"/"}]]`, ErrFormat},
		{"unbalanced", `[1,2,{},[{"name":"/"}]]]`, ErrFormat},
		{"ncdu binary", "\xbfncduEX1\x00\x00\x00\x01", ErrFormat},
	}
	for _, c := range cases {
		for _, r := range readers {
			root, _, err := r.read([]byte(c.doc))
			if !errors.Is(err, c.want) {
				t.Errorf("%s/%s: err = %v, want %v", c.name, r.name, err, c.want)
			}
			if err != nil && root != nil {
				t.Errorf("%s/%s: tree returned with an error", c.name, r.name)
			}
			if err != nil && !strings.HasPrefix(err.Error(), "import ncdu : ") {
				t.Errorf("%s/%s: error lacks prefix: %v", c.name, r.name, err)
			}
		}
	}
}

// TestReadNcduTruncated cuts valid documents at every byte: each strict
// prefix must fail with ErrTruncated, never yield a partial tree.
func TestReadNcduTruncated(t *testing.T) {
	docs := map[string][]byte{
		"sample":    []byte(ncduSample),
		"roundtrip": writeDoc(t, roundTripTree(), Meta{Progver: "x", Timestamp: 1}),
	}
	for name, doc := range docs {
		end := bytes.LastIndexByte(doc, ']') // the rest is trailing whitespace
		for _, r := range []readerCase{readers[0], readers[1], readers[4]} {
			for i := 0; i < end; i++ {
				root, _, err := r.read(doc[:i])
				if !errors.Is(err, ErrTruncated) {
					t.Fatalf("%s/%s: prefix %d (%q): err = %v, want ErrTruncated", name, r.name, i, doc[max(0, i-20):i], err)
				}
				if root != nil {
					t.Fatalf("%s/%s: prefix %d: tree returned", name, r.name, i)
				}
			}
		}
	}
}

func TestReadNcduReaderError(t *testing.T) {
	errBoom := errors.New("connexion perdue")
	doc := []byte(ncduSample)
	for _, cut := range []int{0, 10, len(doc) / 2} {
		r := io.MultiReader(bytes.NewReader(doc[:cut]), iotest.ErrReader(errBoom))
		if _, _, err := ReadNcdu(r); !errors.Is(err, errBoom) {
			t.Errorf("cut %d: err = %v, want the reader's error", cut, err)
		}
	}
}

func FuzzReadNcdu(f *testing.F) {
	f.Add([]byte(ncduSample))
	f.Add(writeDoc(f, roundTripTree(), Meta{Timestamp: 1}))
	f.Add([]byte(`[1,2,{},[{"name":"/"},[{"name":"a"},{"name":"b","dsize":1}]]]`))
	f.Fuzz(func(t *testing.T, doc []byte) {
		root, _, err := ReadNcdu(bytes.NewReader(doc))
		if err != nil {
			return
		}
		// Anything accepted must survive our own round trip.
		again, _, err := ReadNcdu(bytes.NewReader(writeDoc(t, root, Meta{Timestamp: 1})))
		if err != nil {
			t.Fatalf("re-reading our export: %v", err)
		}
		if again.Size != root.Size || again.FileCount != root.FileCount || again.DirCount != root.DirCount {
			t.Fatalf("round trip changed totals: %d/%d/%d -> %d/%d/%d",
				root.Size, root.FileCount, root.DirCount, again.Size, again.FileCount, again.DirCount)
		}
	})
}

// TestNcduBinaryReadsOurExport feeds our export to a real ncdu, if one is
// installed, and checks that ncdu's re-export describes the same tree.
func TestNcduBinaryReadsOurExport(t *testing.T) {
	bin, err := exec.LookPath("ncdu")
	if err != nil {
		t.Skip("ncdu not installed")
	}
	want := roundTripTree()
	dir := t.TempDir()
	in := filepath.Join(dir, "in.json")
	out := filepath.Join(dir, "out.json")
	if err := os.WriteFile(in, writeDoc(t, want, Meta{Progver: "test", Timestamp: 1700000000}), 0o600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	// -0: no UI at all; -e: keep extended info (mtime); -f in -o out: import then re-export.
	if b, err := exec.CommandContext(ctx, bin, "-0", "-e", "-f", in, "-o", out).CombinedOutput(); err != nil {
		t.Fatalf("ncdu rejected our export: %v\n%s", err, b)
	}
	f, err := os.Open(out)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	got, _, err := ReadNcdu(f)
	if err != nil {
		t.Fatalf("reading ncdu's re-export: %v", err)
	}
	// ncdu may reorder entries: compare by path.
	w, g := summarize(want, noHardlinkBit), summarize(got, noHardlinkBit)
	for path, ws := range w {
		if gs, ok := g[path]; !ok || gs != ws {
			t.Errorf("%q: ncdu re-export %+v (present=%v), want %+v", path, gs, ok, ws)
		}
	}
	if len(g) != len(w) {
		t.Errorf("ncdu re-export has %d entries, want %d", len(g), len(w))
	}
}

// genTree builds a tree of top×sub directories holding files files each.
func genTree(top, sub, files int) *scanner.Node {
	root := dir("/scale", 4096, 1700000000, 0)
	for i := range top {
		d := dir("dir-"+strconv.Itoa(i), 4096, 1700000000, 0)
		d.Parent = root
		root.Children = append(root.Children, d)
		for j := range sub {
			s := dir("sub-"+strconv.Itoa(j), 4096, 1700000000+int64(j), 0)
			s.Parent = d
			d.Children = append(d.Children, s)
			s.Children = make([]*scanner.Node, files)
			for k := range files {
				f := file("file-"+strconv.Itoa(k)+".dat", int64((i*7+j*3+k)%64)*4096, 1700000000+int64(k), 0)
				f.Parent = s
				s.Children[k] = f
			}
		}
	}
	testAggregate(root)
	return root
}

func TestNcduScale(t *testing.T) {
	top, sub, files := 200, 10, 100 // 200k files + 2,201 directories
	if testing.Short() {
		top = 20
	}
	want := genTree(top, sub, files)
	nodes := int(want.FileCount) + int(want.DirCount) + 1

	writeAllocs := testing.AllocsPerRun(1, func() {
		if err := WriteNcdu(io.Discard, want, Meta{Timestamp: 1}); err != nil {
			t.Fatal(err)
		}
	})
	start := time.Now()
	doc := writeDoc(t, want, Meta{Timestamp: 1})
	writeTime := time.Since(start)

	runtime.GC()
	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	start = time.Now()
	got, _, err := ReadNcdu(bytes.NewReader(doc))
	readTime := time.Since(start)
	runtime.ReadMemStats(&after)
	if err != nil {
		t.Fatal(err)
	}
	allocs := float64(after.Mallocs-before.Mallocs) / float64(nodes)
	t.Logf("%d nodes, %.1f MB: write %v (%.0f allocs total), read %v (%.1f allocs/node, %.0f bytes/node allocated)",
		nodes, float64(len(doc))/1e6, writeTime, writeAllocs, readTime, allocs,
		float64(after.TotalAlloc-before.TotalAlloc)/float64(nodes))

	compareTrees(t, want, got, allFlags)
	// Generous bounds: they catch accidental per-node blowups, not jitter.
	if writeAllocs > 100 {
		t.Errorf("WriteNcdu made %.0f allocations; it should not allocate per node", writeAllocs)
	}
	if allocs > 25 {
		t.Errorf("ReadNcdu made %.1f allocations per node", allocs)
	}
}

func BenchmarkWriteNcdu(b *testing.B) {
	root := genTree(50, 10, 100)
	doc := writeDoc(b, root, Meta{Timestamp: 1})
	b.SetBytes(int64(len(doc)))
	b.ReportAllocs()
	for b.Loop() {
		if err := WriteNcdu(io.Discard, root, Meta{Timestamp: 1}); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkReadNcdu(b *testing.B) {
	doc := writeDoc(b, genTree(50, 10, 100), Meta{Timestamp: 1})
	b.SetBytes(int64(len(doc)))
	b.ReportAllocs()
	for b.Loop() {
		if _, _, err := ReadNcdu(bytes.NewReader(doc)); err != nil {
			b.Fatal(err)
		}
	}
}

// scan runs scanner.Scan to completion.
func scan(t testing.TB, path string) *scanner.Node {
	t.Helper()
	var root *scanner.Node
	for p := range scanner.Scan(context.Background(), path, scanner.ScanOptions{}) {
		if p.Done {
			root = p.Root
		}
	}
	if root == nil {
		t.Fatalf("scan of %s returned no tree", path)
	}
	return root
}

// TestNcduRoundTripRealScan round-trips a tree produced by the real
// scanner, hard link and symlink included.
func TestNcduRoundTripRealScan(t *testing.T) {
	base := t.TempDir()
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	must(os.MkdirAll(filepath.Join(base, "a", "b", "c"), 0o755))
	must(os.MkdirAll(filepath.Join(base, "é \"x\""), 0o755))
	must(os.WriteFile(filepath.Join(base, "a", "big"), bytes.Repeat([]byte("x"), 100_000), 0o644))
	must(os.WriteFile(filepath.Join(base, "a", "b", "c", "small"), []byte("y"), 0o644))
	must(os.WriteFile(filepath.Join(base, "é \"x\"", "line\nbreak"), []byte("z"), 0o644))
	must(os.Link(filepath.Join(base, "a", "big"), filepath.Join(base, "a", "b", "hardlink")))
	must(os.Symlink("a/big", filepath.Join(base, "sym")))

	want := scan(t, base)
	if want.FileCount != 5 || want.DirCount != 4 {
		t.Fatalf("scan: %d files, %d dirs; want 5, 4", want.FileCount, want.DirCount)
	}
	got, _, err := ReadNcdu(bytes.NewReader(writeDoc(t, want, Meta{Timestamp: 1})))
	if err != nil {
		t.Fatal(err)
	}
	compareTrees(t, want, got, noHardlinkBit)
}
