package export

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/D1nma/disk_check/internal/scanner"
)

// PromOptions controls WriteProm.
type PromOptions struct {
	MaxDepth int               // directories down to this depth below root (root = depth 0); <=0 → 2
	MinBytes int64             // skip directories smaller than this; <0 → 0, 0 → default 100 MiB
	Labels   map[string]string // extra constant labels (e.g. host), sorted by key in output
	// Timestamp is the value of disk_explorer_scan_timestamp_seconds, in
	// unix seconds: when the scan finished. 0 → the current time.
	Timestamp int64
}

// Defaults applied by WriteProm.
const (
	DefaultPromMaxDepth       = 2
	DefaultPromMinBytes int64 = 100 << 20
)

// Metric names written by WriteProm.
const (
	MetricDirectoryBytes      = "disk_explorer_directory_bytes"
	MetricDirectoryFiles      = "disk_explorer_directory_files"
	MetricDirectoryIncomplete = "disk_explorer_directory_incomplete"
	MetricScanTimestamp       = "disk_explorer_scan_timestamp_seconds"
)

// WriteProm writes Prometheus text exposition format (textfile collector).
//
// For root and every directory down to opts.MaxDepth whose Size is at least
// opts.MinBytes (root is always included; a directory below the threshold
// is skipped with its subtree, since children are never larger), it writes
// three gauges labelled with the directory's path: its du-style disk usage
// (Size), its file count (FileCount) and whether it is incomplete
// (unreadable or partial). Directories are listed depth-first, siblings
// sorted by name (byte order), so the output is deterministic. A final
// gauge gives opts.Timestamp. Labels are opts.Labels sorted by name, then
// "path"; label values are escaped as the format requires and invalid UTF-8
// is replaced by U+FFFD. Should two paths collide after that replacement,
// only the first is written, as duplicate series would make node_exporter
// reject the whole file.
//
// An invalid label name (not [a-zA-Z_][a-zA-Z0-9_]*, starting with "__", or
// "path") is an error and nothing is written.
func WriteProm(w io.Writer, root *scanner.Node, opts PromOptions) error {
	if root == nil {
		return errors.New("export Prometheus : arbre vide")
	}
	maxDepth := opts.MaxDepth
	if maxDepth <= 0 {
		maxDepth = DefaultPromMaxDepth
	}
	minBytes := opts.MinBytes
	if minBytes < 0 {
		minBytes = 0
	} else if minBytes == 0 {
		minBytes = DefaultPromMinBytes
	}
	ts := opts.Timestamp
	if ts == 0 {
		ts = time.Now().Unix()
	}
	constLabels, err := promConstLabels(opts.Labels)
	if err != nil {
		return err
	}
	dirs := promSelect(root, maxDepth, minBytes, constLabels)

	bw := bufio.NewWriter(w)
	var line []byte
	family := func(name, help string, value func(*scanner.Node) int64) error {
		line = append(line[:0], "# HELP "...)
		line = append(line, name...)
		line = append(line, ' ')
		line = append(line, help...)
		line = append(line, "\n# TYPE "...)
		line = append(line, name...)
		line = append(line, " gauge\n"...)
		for _, d := range dirs {
			line = append(line, name...)
			line = append(line, d.labels...)
			line = append(line, ' ')
			line = strconv.AppendInt(line, value(d.n), 10)
			line = append(line, '\n')
			if len(line) > 32<<10 {
				if _, err := bw.Write(line); err != nil {
					return err
				}
				line = line[:0]
			}
		}
		_, err := bw.Write(line)
		return err
	}
	if err := family(MetricDirectoryBytes, "Disk usage of a directory, du-style (bytes).",
		func(n *scanner.Node) int64 { return n.Size }); err != nil {
		return err
	}
	if err := family(MetricDirectoryFiles, "Number of files below a directory.",
		func(n *scanner.Node) int64 { return int64(n.FileCount) }); err != nil {
		return err
	}
	if err := family(MetricDirectoryIncomplete, "1 if part of the directory could not be read.",
		func(n *scanner.Node) int64 {
			if n.Incomplete() {
				return 1
			}
			return 0
		}); err != nil {
		return err
	}

	line = append(line[:0], "# HELP "+MetricScanTimestamp+" Unix time when the scan finished.\n"...)
	line = append(line, "# TYPE "+MetricScanTimestamp+" gauge\n"...)
	line = append(line, MetricScanTimestamp...)
	if len(constLabels) > 0 {
		line = append(line, '{')
		line = append(line, constLabels...)
		line = append(line, '}')
	}
	line = append(line, ' ')
	line = strconv.AppendInt(line, ts, 10)
	line = append(line, '\n')
	if _, err := bw.Write(line); err != nil {
		return err
	}
	return bw.Flush()
}

// promDir is one selected directory with its rendered label set.
type promDir struct {
	n      *scanner.Node
	labels []byte // {const...,path="..."}
}

// promSelect lists root and the qualifying directories below it, depth-first
// with siblings sorted by name, dropping label sets already seen.
func promSelect(root *scanner.Node, maxDepth int, minBytes int64, constLabels []byte) []promDir {
	type item struct {
		n     *scanner.Node
		path  string
		depth int
	}
	var dirs []promDir
	seen := make(map[string]struct{})
	stack := []item{{n: root, path: root.Path()}}
	var kids []*scanner.Node
	for len(stack) > 0 {
		it := stack[len(stack)-1]
		stack = stack[:len(stack)-1]

		labels := append([]byte{'{'}, constLabels...)
		if len(constLabels) > 0 {
			labels = append(labels, ',')
		}
		labels = append(labels, `path="`...)
		labels = appendLabelValue(labels, it.path)
		labels = append(labels, `"}`...)
		if _, dup := seen[string(labels)]; !dup {
			seen[string(labels)] = struct{}{}
			dirs = append(dirs, promDir{n: it.n, labels: labels})
		}

		if it.depth >= maxDepth {
			continue
		}
		kids = kids[:0]
		for _, c := range it.n.Children {
			if c != nil && c.IsDir && c.Size >= minBytes {
				kids = append(kids, c)
			}
		}
		slices.SortStableFunc(kids, func(a, b *scanner.Node) int { return strings.Compare(a.Name, b.Name) })
		// Pushed in reverse so the smallest name is visited first.
		for i := len(kids) - 1; i >= 0; i-- {
			stack = append(stack, item{n: kids[i], path: joinPath(it.path, kids[i].Name), depth: it.depth + 1})
		}
	}
	return dirs
}

// joinPath joins like scanner.Node.Path.
func joinPath(dir, name string) string {
	if dir == string(os.PathSeparator) {
		return dir + name
	}
	return dir + string(os.PathSeparator) + name
}

// promConstLabels validates the extra labels and renders them sorted by
// name as `a="x",b="y"`.
func promConstLabels(labels map[string]string) ([]byte, error) {
	names := make([]string, 0, len(labels))
	for name := range labels {
		if !validLabelName(name) {
			return nil, fmt.Errorf("export Prometheus : nom de label invalide %q", name)
		}
		names = append(names, name)
	}
	slices.Sort(names)
	var b []byte
	for i, name := range names {
		if i > 0 {
			b = append(b, ',')
		}
		b = append(b, name...)
		b = append(b, '=', '"')
		b = appendLabelValue(b, labels[name])
		b = append(b, '"')
	}
	return b, nil
}

// validLabelName accepts [a-zA-Z_][a-zA-Z0-9_]*, minus the reserved "__"
// prefix and the "path" label WriteProm sets itself.
func validLabelName(name string) bool {
	if name == "" || name == "path" || strings.HasPrefix(name, "__") {
		return false
	}
	for i := 0; i < len(name); i++ {
		c := name[i]
		switch {
		case c == '_', 'a' <= c && c <= 'z', 'A' <= c && c <= 'Z':
		case '0' <= c && c <= '9' && i > 0:
		default:
			return false
		}
	}
	return true
}

// appendLabelValue appends s escaped for a label value: backslash, double
// quote and line feed are escaped; invalid UTF-8 becomes U+FFFD.
func appendLabelValue(dst []byte, s string) []byte {
	for i := 0; i < len(s); {
		c := s[i]
		switch {
		case c == '\\':
			dst = append(dst, '\\', '\\')
		case c == '"':
			dst = append(dst, '\\', '"')
		case c == '\n':
			dst = append(dst, '\\', 'n')
		case c < utf8.RuneSelf:
			dst = append(dst, c)
		default:
			r, size := utf8.DecodeRuneInString(s[i:])
			if r == utf8.RuneError && size == 1 {
				dst = utf8.AppendRune(dst, utf8.RuneError)
			} else {
				dst = append(dst, s[i:i+size]...)
			}
			i += size
			continue
		}
		i++
	}
	return dst
}
