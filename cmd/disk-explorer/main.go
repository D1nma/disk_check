package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
	"time"

	"github.com/D1nma/disk_check/internal/annotate"
	"github.com/D1nma/disk_check/internal/display"
	"github.com/D1nma/disk_check/internal/reconcile"
	"github.com/D1nma/disk_check/internal/scanner"
	"github.com/D1nma/disk_check/internal/snapshot"
	"github.com/D1nma/disk_check/internal/tui"
	"github.com/D1nma/disk_check/internal/updater"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/term"
)

var version = "dev"

// Exit codes. 2 matches the Bash implementation's "partial scan" code, so
// usage errors use 64 (EX_USAGE) to stay distinguishable.
const (
	exitOK      = 0
	exitFatal   = 1
	exitPartial = 2
	exitUsage   = 64
)

type config struct {
	mode        string
	excludes    []string
	doSummary   bool
	doReport    bool
	doTree      bool
	doJSON      bool
	doWhy       bool
	doUpdate    bool
	showVersion bool
	treeDepth   int
	topN        int
	reportDir   string
	scanWorkers int
	noSnapshot  bool
	readOnly    bool
	path        string
}

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

func parseArgs(args []string, stderr io.Writer) (*config, error) {
	cfg := &config{}
	fs := flag.NewFlagSet("disk-explorer", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.BoolVar(&cfg.showVersion, "version", false, "Affiche la version et quitte")
	fs.BoolVar(&cfg.doUpdate, "update", false, "Télécharge et installe la dernière version, puis quitte")
	fs.StringVar(&cfg.mode, "mode", "global", "global (tous les systèmes de fichiers) ou partition (même périphérique, comme du -x)")
	fs.Func("exclude", "Chemin à ignorer (répétable)", func(path string) error {
		if path == "" {
			return fmt.Errorf("le chemin à exclure ne peut pas être vide")
		}
		abs, err := filepath.Abs(path)
		if err != nil {
			return err
		}
		cfg.excludes = append(cfg.excludes, abs)
		return nil
	})
	fs.BoolVar(&cfg.doSummary, "summary", false, "Affiche un résumé et quitte")
	fs.BoolVar(&cfg.doReport, "report", false, "Écrit le résumé dans un fichier et quitte")
	fs.BoolVar(&cfg.doTree, "tree", false, "Affiche l'arborescence et quitte")
	fs.BoolVar(&cfg.doJSON, "json", false, "Résumé au format JSON sur la sortie standard")
	fs.BoolVar(&cfg.doWhy, "why", false, "Explique l'écart entre df et le contenu réel du système de fichiers de PATH, puis quitte")
	fs.IntVar(&cfg.treeDepth, "tree-depth", 3, "Profondeur max pour --tree")
	fs.IntVar(&cfg.topN, "top", 20, "Nombre d'entrées dans --summary/--report/--json")
	fs.StringVar(&cfg.reportDir, "report-dir", ".", "Dossier de sortie pour --report")
	fs.IntVar(&cfg.scanWorkers, "scan-workers", 0, "Workers de scan max (0 = automatique, 1-64 = limite fixe)")
	fs.BoolVar(&cfg.noSnapshot, "no-snapshot", false, "N'enregistre pas d'instantané (historique de croissance)")
	fs.BoolVar(&cfg.readOnly, "read-only", false, "TUI : interdit la suppression")
	if err := fs.Parse(args); err != nil {
		return nil, err
	}
	if cfg.scanWorkers < 0 || cfg.scanWorkers > 64 {
		return nil, fmt.Errorf("--scan-workers doit être entre 0 et 64")
	}
	if cfg.mode != "global" && cfg.mode != "partition" {
		return nil, fmt.Errorf("--mode invalide %q (global ou partition)", cfg.mode)
	}
	if cfg.topN < 1 {
		return nil, fmt.Errorf("--top doit être >= 1")
	}
	cfg.path = "."
	switch rest := fs.Args(); len(rest) {
	case 0:
	case 1:
		cfg.path = rest[0]
	default:
		return nil, fmt.Errorf("un seul chemin attendu, reçu : %s", strings.Join(rest, " "))
	}
	return cfg, nil
}

func run(args []string, stdout, stderr io.Writer) int {
	cfg, err := parseArgs(args, stderr)
	if err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return exitOK
		}
		if !strings.HasPrefix(err.Error(), "flag ") { // flag errors are already printed with usage
			fmt.Fprintf(stderr, "Erreur : %v\n", err)
		}
		return exitUsage
	}

	if cfg.showVersion {
		fmt.Fprintf(stdout, "disk-explorer %s\n", version)
		return exitOK
	}
	if cfg.doUpdate {
		return runUpdate(stdout, stderr)
	}

	opts := scanner.ScanOptions{
		SameDevice: cfg.mode == "partition",
		Excludes:   cfg.excludes,
		Workers:    cfg.scanWorkers,
	}

	switch {
	case cfg.doWhy:
		return runWhy(stdout, stderr, cfg, opts)
	case cfg.doSummary || cfg.doJSON:
		return runSummary(stdout, stderr, cfg, opts)
	case cfg.doReport:
		return runReport(stdout, stderr, cfg, opts)
	case cfg.doTree:
		return runTree(stdout, stderr, cfg, opts)
	case !isTerminal(stdout):
		// Piped or redirected output gets the summary, like the Bash version.
		return runSummary(stdout, stderr, cfg, opts)
	default:
		return runTUI(stderr, cfg, opts)
	}
}

func isTerminal(w io.Writer) bool {
	f, ok := w.(*os.File)
	return ok && term.IsTerminal(f.Fd())
}

func runUpdate(stdout, stderr io.Writer) int {
	tag, err := updater.LatestRelease()
	if err != nil {
		fmt.Fprintf(stderr, "Erreur : impossible de vérifier la dernière version : %v\n", err)
		return exitFatal
	}
	if !updater.Newer(tag, version) {
		fmt.Fprintf(stdout, "Déjà à jour (%s ; dernière publiée : %s)\n", version, tag)
		return exitOK
	}
	if err := updater.SelfUpdate(tag); err != nil {
		fmt.Fprintf(stderr, "Erreur de mise à jour : %v\n", err)
		return exitFatal
	}
	return exitOK
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

// snapshotID names a scan for the history: same root, mode and exclusions.
func snapshotID(abs string, excludes []string) string {
	if len(excludes) == 0 {
		return abs
	}
	ex := append([]string(nil), excludes...)
	sort.Strings(ex)
	return abs + "|exclude:" + strings.Join(ex, ":")
}

// history bundles the snapshot store and the previous snapshot of a root.
type history struct {
	store *snapshot.Store // nil when disabled
	id    string
	mode  string
	prev  *snapshot.Snapshot
}

func loadHistory(cfg *config, abs string) history {
	h := history{id: snapshotID(abs, cfg.excludes), mode: cfg.mode}
	if cfg.noSnapshot || !snapshot.Enabled() {
		return h
	}
	st, err := snapshot.DefaultStore()
	if err != nil {
		return h
	}
	h.store = &st
	h.prev, _ = st.Latest(h.id, h.mode)
	return h
}

func (h history) save(res scanner.ScanProgress, stderr io.Writer) {
	if h.store == nil || res.Root == nil {
		return
	}
	snap := snapshot.FromTree(res.Root, h.id, h.mode, int64(res.Files+res.Dirs), time.Now())
	if err := h.store.Save(snap); err != nil {
		fmt.Fprintf(stderr, "Avertissement : instantané non enregistré : %v\n", err)
	}
}

// scanAll runs a synchronous scan with history: the previous snapshot gives
// the expected entry count, the new one is saved afterwards.
func scanAll(cfg *config, opts scanner.ScanOptions, stderr io.Writer) (scanner.ScanProgress, history, error) {
	abs, err := filepath.Abs(cfg.path)
	if err != nil {
		abs = cfg.path
	}
	h := loadHistory(cfg, abs)
	if h.prev != nil {
		opts.ExpectedHint = h.prev.Entries
	}
	var last scanner.ScanProgress
	for p := range scanner.Scan(context.Background(), cfg.path, opts) {
		last = p
	}
	if last.Err != nil {
		return last, h, last.Err
	}
	if last.Root == nil {
		return last, h, fmt.Errorf("%s : scan interrompu", abs)
	}
	h.save(last, stderr)
	return last, h, nil
}

func summaryData(res scanner.ScanProgress, h history, cfg *config) display.SummaryData {
	d := display.SummaryData{
		Root:       res.Root,
		TopFiles:   res.TopFiles,
		Disk:       getDiskInfo(cfg.path),
		TopN:       cfg.topN,
		ErrorCount: res.ErrorCount,
		Errors:     res.Errors,
		Recent:     res.Recent[res.Root],
		Pinned:     res.Pinned,
		Annotate:   summaryAnnotator(),
	}
	if cfg.topN > len(res.TopFiles) {
		d.TopFiles = scanner.TopFiles(res.Root, cfg.topN)
	}
	if h.prev != nil {
		d.Since = h.prev.Time
		d.Growth = h.prev.TopGrowth(res.Root, cfg.topN)
	}
	return d
}

func summaryAnnotator() func(string, bool) (string, bool) {
	ann := annotate.New(annotate.Options{})
	return func(path string, isDir bool) (string, bool) {
		a, ok := ann.Lookup(path, isDir)
		if !ok {
			return "", false
		}
		label := a.Label
		if a.Cleanup != "" {
			label += " → " + a.Cleanup
		}
		return label, true
	}
}

func exitFor(res scanner.ScanProgress) int {
	if res.ErrorCount > 0 {
		return exitPartial
	}
	return exitOK
}

func runSummary(stdout, stderr io.Writer, cfg *config, opts scanner.ScanOptions) int {
	res, h, err := scanAll(cfg, opts, stderr)
	if err != nil {
		fmt.Fprintf(stderr, "Erreur : %v\n", err)
		return exitFatal
	}
	d := summaryData(res, h, cfg)
	if cfg.doJSON {
		if err := display.SummaryJSON(stdout, d); err != nil {
			fmt.Fprintf(stderr, "Erreur : %v\n", err)
			return exitFatal
		}
	} else {
		display.Summary(stdout, d)
	}
	return exitFor(res)
}

func runReport(stdout, stderr io.Writer, cfg *config, opts scanner.ScanOptions) int {
	if err := os.MkdirAll(cfg.reportDir, 0o755); err != nil {
		fmt.Fprintf(stderr, "Erreur : %v\n", err)
		return exitFatal
	}
	res, h, err := scanAll(cfg, opts, stderr)
	if err != nil {
		fmt.Fprintf(stderr, "Erreur : %v\n", err)
		return exitFatal
	}

	safeName := filepath.Base(res.Root.Path())
	if safeName == "" || safeName == "." || safeName == "/" {
		safeName = "root"
	}
	ts := time.Now().Format("2006-01-02_15-04-05")
	reportPath := filepath.Join(cfg.reportDir, fmt.Sprintf("Report_%s_%s.txt", safeName, ts))

	tmp, err := os.CreateTemp(cfg.reportDir, ".report-*.tmp")
	if err != nil {
		fmt.Fprintf(stderr, "Erreur : %v\n", err)
		return exitFatal
	}
	display.Summary(tmp, summaryData(res, h, cfg))
	if err := tmp.Close(); err != nil {
		os.Remove(tmp.Name())
		fmt.Fprintf(stderr, "Erreur : %v\n", err)
		return exitFatal
	}
	if err := os.Rename(tmp.Name(), reportPath); err != nil {
		os.Remove(tmp.Name())
		fmt.Fprintf(stderr, "Erreur : %v\n", err)
		return exitFatal
	}
	fmt.Fprintf(stdout, "Rapport créé : %s\n", reportPath)
	return exitFor(res)
}

func runTree(stdout, stderr io.Writer, cfg *config, opts scanner.ScanOptions) int {
	res, _, err := scanAll(cfg, opts, stderr)
	if err != nil {
		fmt.Fprintf(stderr, "Erreur : %v\n", err)
		return exitFatal
	}
	display.Tree(stdout, res.Root, cfg.treeDepth)
	if res.ErrorCount > 0 {
		fmt.Fprintf(stderr, "Avertissement : %d dossier(s) illisible(s), tailles sous-estimées\n", res.ErrorCount)
	}
	return exitFor(res)
}

// runWhy scans the whole filesystem holding PATH (from its mount point,
// staying on that device) and explains the gap with what df reports.
func runWhy(stdout, stderr io.Writer, cfg *config, opts scanner.ScanOptions) int {
	fs, err := reconcile.Stat(cfg.path)
	if err != nil {
		fmt.Fprintf(stderr, "Erreur : %s : %v\n", cfg.path, err)
		return exitFatal
	}
	opts.SameDevice = true
	var res scanner.ScanProgress
	for p := range scanner.Scan(context.Background(), fs.MountPoint, opts) {
		res = p
	}
	if res.Err != nil || res.Root == nil {
		fmt.Fprintf(stderr, "Erreur : %v\n", res.Err)
		return exitFatal
	}
	r, err := explainGap(cfg.path, fs.MountPoint, true, res.DevBytes, res.ErrorCount)
	if err != nil {
		fmt.Fprintf(stderr, "Erreur : %v\n", err)
		return exitFatal
	}
	reconcile.Render(stdout, r, 20)
	return exitFor(res)
}

func runTUI(stderr io.Writer, cfg *config, opts scanner.ScanOptions) int {
	abs, err := filepath.Abs(cfg.path)
	if err != nil {
		abs = cfg.path
	}
	h := loadHistory(cfg, abs)
	if h.prev != nil {
		opts.ExpectedHint = h.prev.Entries
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ch := scanner.Scan(ctx, cfg.path, opts)

	// Background update check — notifies TUI model when a new version is found
	updateCh := make(chan string, 1)
	if updater.CheckEnabled() {
		go func() {
			if latest, ok := updater.UpdateAvailable(version); ok {
				updateCh <- latest
			}
		}()
	}

	m := tui.Model{
		Path:        abs,
		Version:     version,
		State:       tui.StateScanning,
		ScannerChan: ch,
		UpdateChan:  updateCh,
		CancelScan:  cancel,
		ScanOpts:    opts,
		ReadOnly:    cfg.readOnly,
		Hooks:       localHooks(),
		Prev:        h.prev,
		SaveSnapshot: func(res scanner.ScanProgress) {
			h.save(res, io.Discard)
		},
	}

	p := tea.NewProgram(m, tea.WithAltScreen(), tea.WithInputTTY())
	finalModel, err := p.Run()
	if err != nil {
		fmt.Fprintf(stderr, "Erreur : %v\n", err)
		return exitFatal
	}

	fm, ok := finalModel.(tui.Model)
	if !ok {
		return exitOK
	}
	if fm.FatalErr != nil {
		fmt.Fprintf(stderr, "Erreur : %v\n", fm.FatalErr)
		return exitFatal
	}
	if fm.PendingUpdate == "" {
		return exitOK
	}
	// The user accepted the update: the TUI has exited and the terminal is
	// restored. Install the prefetched binary and re-exec in place.
	if err := updater.Install(fm.PendingUpdate); err != nil {
		fmt.Fprintf(stderr, "Erreur pendant l'installation de la mise à jour : %v\n", err)
		return exitFatal
	}
	exe, err := os.Executable()
	if err != nil {
		exe = os.Args[0]
	}
	fmt.Println("Mise à jour installée — relance de disk-explorer…")
	if err := syscall.Exec(exe, os.Args, os.Environ()); err != nil {
		fmt.Fprintf(stderr, "Erreur au redémarrage (%v) — relancez disk-explorer manuellement\n", err)
		return exitFatal
	}
	return exitOK
}
