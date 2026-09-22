package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"syscall"
	"time"

	"github.com/D1nma/disk_check/internal/display"
	"github.com/D1nma/disk_check/internal/scanner"
	"github.com/D1nma/disk_check/internal/tui"
	"github.com/D1nma/disk_check/internal/updater"
	tea "github.com/charmbracelet/bubbletea"
)

var version = "dev"

func main() {
	var (
		mode        string
		excludes    []string
		doSummary   bool
		doReport    bool
		doTree      bool
		doUpdate    bool
		showVersion bool
		treeDepth   int
		topN        int
		reportDir   string
	)

	flag.BoolVar(&showVersion, "version", false, "Print version and exit")
	flag.BoolVar(&doUpdate, "update", false, "Download the latest release and exit")
	flag.StringVar(&mode, "mode", "global", "Analysis mode: global (all filesystems) or partition (same device only)")
	flag.Func("exclude", "Absolute or relative path to skip (repeatable)", func(path string) error {
		if path == "" {
			return fmt.Errorf("exclude path cannot be empty")
		}
		abs, err := filepath.Abs(path)
		if err != nil {
			return err
		}
		excludes = append(excludes, abs)
		return nil
	})
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

	if doUpdate {
		tag, err := updater.LatestRelease()
		if err != nil {
			fmt.Fprintf(os.Stderr, "Erreur: impossible de vérifier la dernière version: %v\n", err)
			os.Exit(1)
		}
		if tag == version {
			fmt.Printf("Déjà à jour (%s)\n", version)
			return
		}
		if err := updater.SelfUpdate(tag); err != nil {
			fmt.Fprintf(os.Stderr, "Erreur de mise à jour: %v\n", err)
			os.Exit(1)
		}
		return
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

func collectRoot(path string, opts scanner.ScanOptions) scanner.ScanProgress {
	ch := scanner.Scan(context.Background(), path, opts)
	var lastProgress scanner.ScanProgress
	for p := range ch {
		lastProgress = p
	}
	return lastProgress
}

func writeSummary(w io.Writer, path string, opts scanner.ScanOptions, topN int) {
	res := collectRoot(path, opts)
	root := res.Root
	var entries []*scanner.Node
	var topFiles []*scanner.Node
	if root != nil {
		sort.Slice(root.Children, func(i, j int) bool {
			return root.Children[i].Size > root.Children[j].Size
		})
		entries = root.Children
		if topN <= len(res.TopFiles) {
			topFiles = res.TopFiles[:topN]
		} else {
			topFiles = scanner.TopFiles(root, topN)
		}
	}
	di := getDiskInfo(path)
	display.Summary(w, path, root, entries, topFiles, di, topN)
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

	// Background update check — notifies TUI model when a new version is found
	updateCh := make(chan string, 1)
	go func() {
		if latest, ok := updater.UpdateAvailable(version); ok {
			updateCh <- latest
		}
	}()

	m := tui.Model{
		Path:        path,
		Version:     version,
		State:       tui.StateScanning,
		ScannerChan: ch,
		UpdateChan:  updateCh,
		CancelScan:  cancel,
		ScanOpts:    opts,
	}

	p := tea.NewProgram(m, tea.WithAltScreen(), tea.WithInputTTY())
	if _, err := p.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}
