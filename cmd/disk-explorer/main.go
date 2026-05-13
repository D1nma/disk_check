package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
	"time"

	"github.com/D1nma/disk_check/internal/display"
	"github.com/D1nma/disk_check/internal/scanner"
	"github.com/D1nma/disk_check/internal/tui"
	tea "github.com/charmbracelet/bubbletea"
)

var version = "dev"

func main() {
	var (
		mode        string
		excludeFlag string
		doSummary   bool
		doReport    bool
		doTree      bool
		treeDepth   int
		topN        int
		reportDir   string
	)

	var showVersion bool
	flag.BoolVar(&showVersion, "version", false, "Print version and exit")
	flag.StringVar(&mode, "mode", "global", "Analysis mode: global (all filesystems) or partition (same device only)")
	flag.BoolVar(&doSummary, "summary", false, "Print disk summary and exit")
	flag.BoolVar(&doReport, "report", false, "Write report to file and exit")
	flag.BoolVar(&doTree, "tree", false, "Print tree view and exit")
	flag.IntVar(&treeDepth, "tree-depth", 3, "Max depth for --tree")
	flag.IntVar(&topN, "top", 20, "Number of top entries to show in --summary/--report")
	flag.StringVar(&reportDir, "report-dir", ".", "Output directory for --report")
	flag.Parse()

	if showVersion {
		fmt.Printf("disk-explorer %s\n", version)
		return
	}

	var excludes []string
	if excludeFlag != "" {
		excludes = strings.Split(excludeFlag, ",")
	}
	for i, arg := range os.Args[1:] {
		if (arg == "--exclude" || arg == "-exclude") && i+1 < len(os.Args)-1 {
			excludes = append(excludes, os.Args[i+2])
		}
	}

	path := "."
	if args := flag.Args(); len(args) > 0 {
		path = args[0]
	}

	opts := scanner.ScanOptions{
		SameDevice: mode == "partition",
		Excludes:   excludes,
	}

	switch {
	case doSummary:
		runSummary(os.Stdout, path, opts, topN)
	case doReport:
		runReport(path, opts, topN, reportDir)
	case doTree:
		runTree(os.Stdout, path, opts, treeDepth)
	default:
		runTUI(path, opts)
	}
}

func getDiskInfo(path string) display.DiskInfo {
	var st syscall.Statfs_t
	if err := syscall.Statfs(path, &st); err != nil {
		return display.DiskInfo{}
	}
	bs := int64(st.Bsize)
	return display.DiskInfo{
		Total: int64(st.Blocks) * bs,
		Used:  (int64(st.Blocks) - int64(st.Bfree)) * bs,
		Avail: int64(st.Bavail) * bs,
	}
}

func collectEntries(path string, opts scanner.ScanOptions) []scanner.Entry {
	var entries []scanner.Entry
	for e := range scanner.Scan(context.Background(), path, opts) {
		entries = append(entries, e)
	}
	sort.Slice(entries, func(i, j int) bool {
		return entries[i].Size > entries[j].Size
	})
	return entries
}

func writeSummary(w io.Writer, path string, opts scanner.ScanOptions, topN int) {
	ctx := context.Background()
	entries := collectEntries(path, opts)
	topFiles := scanner.ScanTopFiles(ctx, path, topN, opts)
	di := getDiskInfo(path)
	display.Summary(w, path, entries, topFiles, di, topN)
}

func runSummary(w io.Writer, path string, opts scanner.ScanOptions, topN int) {
	writeSummary(w, path, opts, topN)
}

func runReport(path string, opts scanner.ScanOptions, topN int, reportDir string) {
	if err := os.MkdirAll(reportDir, 0755); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	safeName := filepath.Base(path)
	if safeName == "" || safeName == "." || safeName == "/" {
		safeName = "root"
	}
	ts := time.Now().Format("2006-01-02_15-04-05")
	reportPath := filepath.Join(reportDir, fmt.Sprintf("Report_%s_%s.txt", safeName, ts))

	tmp := reportPath + ".tmp"
	f, err := os.Create(tmp)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
	writeSummary(f, path, opts, topN)
	f.Close()

	if err := os.Rename(tmp, reportPath); err != nil {
		os.Remove(tmp)
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("Rapport créé : %s\n", reportPath)
}

func runTree(w io.Writer, path string, opts scanner.ScanOptions, maxDepth int) {
	node, err := scanner.ScanTree(context.Background(), path, maxDepth, opts)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
	display.Tree(w, node, maxDepth)
}

func runTUI(path string, opts scanner.ScanOptions) {
	ctx, cancel := context.WithCancel(context.Background())
	ch := scanner.Scan(ctx, path, opts)

	m := tui.Model{
		Path:        path,
		Version:     version,
		Entries:     []scanner.Entry{},
		ScannerChan: ch,
		Scanning:    true,
		CancelScan:  cancel,
		ScanOpts:    opts,
	}

	p := tea.NewProgram(m, tea.WithAltScreen(), tea.WithInputTTY())
	if _, err := p.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}
