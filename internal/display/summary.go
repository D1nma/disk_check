package display

import (
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strings"
	"time"

	"github.com/D1nma/disk_check/internal/scanner"
	"github.com/D1nma/disk_check/internal/snapshot"
	"github.com/D1nma/disk_check/internal/textsafe"
)

type DiskInfo struct {
	Total int64
	Used  int64
	Avail int64
}

// SummaryData is everything a summary report shows.
type SummaryData struct {
	Root       *scanner.Node
	TopFiles   []*scanner.Node // biggest first
	Disk       DiskInfo
	TopN       int
	ErrorCount int
	Errors     []scanner.ScanError
	Recent     int64 // bytes modified in the last 24 h below Root
	Pinned     map[*scanner.Node]int64

	// Growth since the previous snapshot; Since is zero when there is none.
	Growth []snapshot.Growth
	Since  time.Time

	Now time.Time // report time; zero means time.Now()

	// Annotate optionally describes a path (docker container, cache with
	// its cleanup command). The text is sanitized before printing.
	Annotate func(absPath string, isDir bool) (string, bool)
}

func (d SummaryData) note(n *scanner.Node) string {
	if d.Annotate == nil {
		return ""
	}
	if s, ok := d.Annotate(n.Path(), n.IsDir); ok && s != "" {
		return "  · " + textsafe.Clean(s)
	}
	return ""
}

// maxListedErrors is how many unreadable paths a summary lists.
const maxListedErrors = 10

// relPath returns n's path relative to root ("" when n is root).
func relPath(root, n *scanner.Node) string {
	p := n.Path()
	base := root.Path()
	if p == base {
		return ""
	}
	if base == "/" {
		return strings.TrimPrefix(p, "/")
	}
	return strings.TrimPrefix(p, base+"/")
}

func incompleteMark(n *scanner.Node) string {
	switch {
	case n.Flags&scanner.FlagUnreadable != 0:
		return "  ⚠ illisible"
	case n.Flags&scanner.FlagPartial != 0:
		return "  ⚠ incomplet"
	}
	return ""
}

// Summary writes the text report used by --summary and --report.
func Summary(w io.Writer, d SummaryData) {
	now := d.Now
	if now.IsZero() {
		now = time.Now()
	}
	root := d.Root
	fmt.Fprintf(w, "RAPPORT DISQUE - %s\n", now.Format("2006-01-02 15:04:05"))
	if root == nil {
		fmt.Fprintln(w, "  (aucune donnée)")
		return
	}
	fmt.Fprintf(w, "Dossier : %s\n", textsafe.Clean(root.Path()))
	fmt.Fprintf(w, "Taille  : %s  (%d fichiers, %d dossiers)\n",
		FormatSize(root.Size), root.FileCount, root.DirCount)
	if d.Disk.Total > 0 {
		pct := d.Disk.Used * 100 / d.Disk.Total
		fmt.Fprintf(w, "Disque  : %s utilisé / %s total (%d%%)\n",
			FormatSize(d.Disk.Used), FormatSize(d.Disk.Total), pct)
		fmt.Fprintf(w, "Libre   : %s\n", FormatSize(d.Disk.Avail))
		fmt.Fprintf(w, "Part de ce dossier dans le disque : %d%%\n", root.Size*100/d.Disk.Total)
	}
	if d.Recent > 0 {
		fmt.Fprintf(w, "Modifié depuis 24 h : %s\n", FormatSize(d.Recent))
	}
	if d.ErrorCount > 0 {
		fmt.Fprintf(w, "\n⚠ SCAN INCOMPLET : %d dossier(s) illisible(s) — les tailles sont sous-estimées (relancer avec sudo ?)\n", d.ErrorCount)
		for i, e := range d.Errors {
			if i >= maxListedErrors {
				fmt.Fprintf(w, "  … et %d autre(s)\n", d.ErrorCount-maxListedErrors)
				break
			}
			fmt.Fprintf(w, "  %s : %s\n", textsafe.Clean(e.Path), e.Err)
		}
	}
	fmt.Fprintln(w)

	if !d.Since.IsZero() {
		fmt.Fprintf(w, "CROISSANCE DEPUIS LE %s :\n", d.Since.Format("2006-01-02 15:04"))
		if len(d.Growth) == 0 {
			fmt.Fprintln(w, "  (rien n'a grossi de plus de 1 MiB)")
		}
		for _, g := range d.Growth {
			approx := ""
			if !g.Exact {
				approx = "  (nouveau)"
			}
			fmt.Fprintf(w, "  %10s  %s%s\n", "+"+FormatSize(g.Delta), textsafe.Clean(g.Path), approx)
		}
		fmt.Fprintln(w)
	}

	entries := make([]*scanner.Node, 0, len(root.Children))
	for _, c := range root.Children {
		if c.IsDir {
			entries = append(entries, c)
		}
	}
	sort.SliceStable(entries, func(i, j int) bool { return entries[i].Size > entries[j].Size })

	fmt.Fprintf(w, "TOP SOUS-DOSSIERS :\n")
	for i, e := range entries {
		if i >= d.TopN {
			break
		}
		fmt.Fprintf(w, "  %10s  %s/%s%s\n", FormatSize(e.Size), textsafe.Clean(e.Name), incompleteMark(e), d.note(e))
	}
	if len(entries) == 0 {
		fmt.Fprintln(w, "  (aucun)")
	}
	fmt.Fprintln(w)

	fmt.Fprintf(w, "TOP FICHIERS :\n")
	for i, f := range d.TopFiles {
		if i >= d.TopN {
			break
		}
		shared := ""
		if f.Flags&scanner.FlagHardlink != 0 {
			shared = "  (lien dur)"
		}
		fmt.Fprintf(w, "  %10s  %s%s%s\n", FormatSize(f.Size), textsafe.Clean(relPath(root, f)), shared, d.note(f))
	}
	if len(d.TopFiles) == 0 {
		fmt.Fprintln(w, "  (aucun)")
	}
}

// jsonEntry is one directory or file in the JSON summary.
type jsonEntry struct {
	Path       string `json:"path"`
	Bytes      int64  `json:"bytes"`
	Freeable   int64  `json:"freeable_bytes"`
	Files      int32  `json:"files,omitempty"`
	Dirs       int32  `json:"dirs,omitempty"`
	Incomplete bool   `json:"incomplete,omitempty"`
}

type jsonGrowth struct {
	Path  string `json:"path"`
	Delta int64  `json:"delta_bytes"`
	Bytes int64  `json:"bytes"`
	New   bool   `json:"new,omitempty"`
}

type jsonSummary struct {
	Time        time.Time    `json:"time"`
	Path        string       `json:"path"`
	Bytes       int64        `json:"bytes"`
	Files       int32        `json:"files"`
	Dirs        int32        `json:"dirs"`
	RecentBytes int64        `json:"recent_bytes_24h"`
	Disk        *jsonDisk    `json:"disk,omitempty"`
	Incomplete  bool         `json:"incomplete"`
	ErrorCount  int          `json:"unreadable_count"`
	Errors      []jsonError  `json:"unreadable"`
	GrowthSince *time.Time   `json:"growth_since,omitempty"`
	Growth      []jsonGrowth `json:"growth,omitempty"`
	TopDirs     []jsonEntry  `json:"top_dirs"`
	TopFiles    []jsonEntry  `json:"top_files"`
}

type jsonDisk struct {
	Total int64 `json:"total_bytes"`
	Used  int64 `json:"used_bytes"`
	Avail int64 `json:"avail_bytes"`
}

type jsonError struct {
	Path  string `json:"path"`
	Error string `json:"error"`
}

// SummaryJSON writes the same information as Summary as one JSON object
// (raw paths: JSON escaping makes them safe, consumers get the real bytes
// as far as UTF-8 allows).
func SummaryJSON(w io.Writer, d SummaryData) error {
	now := d.Now
	if now.IsZero() {
		now = time.Now()
	}
	root := d.Root
	if root == nil {
		return fmt.Errorf("aucune donnée")
	}
	out := jsonSummary{
		Time: now, Path: root.Path(), Bytes: root.Size, Files: root.FileCount, Dirs: root.DirCount,
		RecentBytes: d.Recent, Incomplete: root.Incomplete(), ErrorCount: d.ErrorCount,
		Errors: []jsonError{}, TopDirs: []jsonEntry{}, TopFiles: []jsonEntry{},
	}
	if d.Disk.Total > 0 {
		out.Disk = &jsonDisk{Total: d.Disk.Total, Used: d.Disk.Used, Avail: d.Disk.Avail}
	}
	for _, e := range d.Errors {
		out.Errors = append(out.Errors, jsonError{Path: e.Path, Error: e.Err})
	}
	if !d.Since.IsZero() {
		since := d.Since
		out.GrowthSince = &since
		for _, g := range d.Growth {
			out.Growth = append(out.Growth, jsonGrowth{Path: g.Path, Delta: g.Delta, Bytes: g.Size, New: !g.Exact})
		}
	}
	dirs := make([]*scanner.Node, 0, len(root.Children))
	for _, c := range root.Children {
		if c.IsDir {
			dirs = append(dirs, c)
		}
	}
	sort.SliceStable(dirs, func(i, j int) bool { return dirs[i].Size > dirs[j].Size })
	for i, c := range dirs {
		if i >= d.TopN {
			break
		}
		out.TopDirs = append(out.TopDirs, jsonEntry{
			Path: c.Path(), Bytes: c.Size, Freeable: scanner.Freeable(c, d.Pinned),
			Files: c.FileCount, Dirs: c.DirCount, Incomplete: c.Incomplete(),
		})
	}
	for i, f := range d.TopFiles {
		if i >= d.TopN {
			break
		}
		out.TopFiles = append(out.TopFiles, jsonEntry{Path: f.Path(), Bytes: f.Size, Freeable: scanner.Freeable(f, d.Pinned)})
	}
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(out)
}
