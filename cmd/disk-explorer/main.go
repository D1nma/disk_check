package main

import (
	"bufio"
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
	"github.com/D1nma/disk_check/internal/export"
	"github.com/D1nma/disk_check/internal/reconcile"
	"github.com/D1nma/disk_check/internal/remote"
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
	pathGiven   bool

	exportFile string // ncdu JSON output ("-" = stdout)
	importFile string // ncdu JSON input ("-" = stdin) instead of scanning
	promFile   string // Prometheus textfile output
	promDepth  int

	targets    []string // --ssh
	hostsFile  string
	sshOpts    []string
	sshSudo    bool
	sshBinary  string
	sshJobs    int
	sshTimeout time.Duration
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
		// Kept as given for --ssh (paths on the remote host); made absolute
		// for local scans in run().
		cfg.excludes = append(cfg.excludes, path)
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
	fs.StringVar(&cfg.exportFile, "export", "", "Écrit le scan au format JSON de ncdu (- = sortie standard)")
	fs.StringVar(&cfg.importFile, "import", "", "Lit un export JSON (ncdu ou disk-explorer, - = entrée standard) au lieu de scanner")
	fs.StringVar(&cfg.promFile, "prom", "", "Écrit des métriques Prometheus (textfile collector de node_exporter)")
	fs.IntVar(&cfg.promDepth, "prom-depth", export.DefaultPromMaxDepth, "Profondeur des dossiers exportés par --prom")
	fs.Func("ssh", "Scanne [utilisateur@]hôte[:/chemin] via SSH, sans rien installer (répétable, ou liste séparée par des virgules)", func(v string) error {
		for _, t := range strings.Split(v, ",") {
			if t = strings.TrimSpace(t); t != "" {
				cfg.targets = append(cfg.targets, t)
			}
		}
		return nil
	})
	fs.StringVar(&cfg.hostsFile, "hosts-file", "", "Fichier d'hôtes pour --ssh, un par ligne (# = commentaire)")
	fs.Func("ssh-opt", "Option passée à ssh(1), répétable (ex. \"-p 2222\", \"-i ~/.ssh/cle\")", func(v string) error {
		cfg.sshOpts = append(cfg.sshOpts, strings.Fields(v)...)
		return nil
	})
	fs.BoolVar(&cfg.sshSudo, "ssh-sudo", false, "Lance le scan distant avec sudo -n (sudo sans mot de passe requis)")
	fs.StringVar(&cfg.sshBinary, "ssh-binary", "", "Binaire disk-explorer à envoyer (sinon détecté selon l'architecture distante)")
	fs.IntVar(&cfg.sshJobs, "ssh-jobs", 4, "Nombre d'hôtes scannés en parallèle")
	fs.DurationVar(&cfg.sshTimeout, "ssh-timeout", 10*time.Second, "Délai de connexion SSH")
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
	if cfg.sshJobs < 1 {
		return nil, fmt.Errorf("--ssh-jobs doit être >= 1")
	}
	cfg.path = "."
	switch rest := fs.Args(); len(rest) {
	case 0:
	case 1:
		cfg.path, cfg.pathGiven = rest[0], true
	default:
		return nil, fmt.Errorf("un seul chemin attendu, reçu : %s", strings.Join(rest, " "))
	}
	if cfg.hostsFile != "" {
		hosts, err := readHostsFile(cfg.hostsFile)
		if err != nil {
			return nil, err
		}
		cfg.targets = append(cfg.targets, hosts...)
	}
	if cfg.importFile != "" && len(cfg.targets) > 0 {
		return nil, fmt.Errorf("--import et --ssh sont incompatibles")
	}
	if cfg.exportFile == "-" && (cfg.doSummary || cfg.doJSON || cfg.doTree) {
		return nil, fmt.Errorf("--export - occupe la sortie standard : incompatible avec --summary/--json/--tree")
	}
	return cfg, nil
}

func readHostsFile(path string) ([]string, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var hosts []string
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := sc.Text()
		if i := strings.IndexByte(line, '#'); i >= 0 {
			line = line[:i]
		}
		if line = strings.TrimSpace(line); line != "" {
			hosts = append(hosts, line)
		}
	}
	return hosts, sc.Err()
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

	remoteMode := len(cfg.targets) > 0
	if !remoteMode {
		for i, ex := range cfg.excludes {
			if abs, err := filepath.Abs(ex); err == nil {
				cfg.excludes[i] = abs
			}
		}
	}
	opts := scanner.ScanOptions{
		SameDevice: cfg.mode == "partition",
		Excludes:   cfg.excludes,
		Workers:    cfg.scanWorkers,
	}

	if cfg.doWhy {
		if remoteMode || cfg.importFile != "" {
			fmt.Fprintln(stderr, "Erreur : --why analyse la machine locale (pas --ssh ni --import)")
			return exitUsage
		}
		return runWhy(stdout, stderr, cfg, opts)
	}
	if len(cfg.targets) > 1 {
		return runFleet(stdout, stderr, cfg)
	}

	wantsOutput := cfg.doSummary || cfg.doJSON || cfg.doTree || cfg.doReport || cfg.exportFile != "" || cfg.promFile != ""
	interactive := !wantsOutput && isTerminal(stdout)

	var src *source
	switch {
	case remoteMode:
		t, err := targetFor(cfg, cfg.targets[0])
		if err != nil {
			fmt.Fprintf(stderr, "Erreur : %v\n", err)
			return exitUsage
		}
		if interactive {
			return runRemoteTUI(stderr, cfg, t)
		}
		src, err = remoteSource(context.Background(), cfg, t, func(s string) { fmt.Fprintln(stderr, s) })
		if err != nil {
			fmt.Fprintf(stderr, "Erreur : %v\n", err)
			return exitFatal
		}
	case cfg.importFile != "":
		src, err = importSource(cfg)
		if err != nil {
			fmt.Fprintf(stderr, "Erreur : %v\n", err)
			return exitFatal
		}
		if interactive {
			return runTUIFromSource(stderr, cfg, src)
		}
	case interactive:
		return runTUI(stderr, cfg, opts)
	default:
		src, err = localSource(cfg, opts, stderr)
		if err != nil {
			fmt.Fprintf(stderr, "Erreur : %v\n", err)
			return exitFatal
		}
	}
	return writeOutputs(stdout, stderr, cfg, src)
}

// targetFor parses an --ssh value; a positional PATH is the default remote path.
func targetFor(cfg *config, s string) (remote.Target, error) {
	if !strings.Contains(s, ":") && cfg.pathGiven {
		s += ":" + cfg.path
	}
	return remote.ParseTarget(s)
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

// ── History ──────────────────────────────────────────────────────────────────

// snapshotID names a scan for the history: same root, mode and exclusions.
func snapshotID(root string, excludes []string) string {
	if len(excludes) == 0 {
		return root
	}
	ex := append([]string(nil), excludes...)
	sort.Strings(ex)
	return root + "|exclude:" + strings.Join(ex, ":")
}

// history bundles the snapshot store and the previous snapshot of a root.
type history struct {
	store *snapshot.Store // nil when disabled
	id    string
	mode  string
	prev  *snapshot.Snapshot
}

func loadHistory(cfg *config, root string) history {
	h := history{id: snapshotID(root, cfg.excludes), mode: cfg.mode}
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

// ── Sources ──────────────────────────────────────────────────────────────────

// source is a complete tree to report on: a local scan, an imported export
// or a remote host.
type source struct {
	res      scanner.ScanProgress
	disk     display.DiskInfo
	hist     history
	label    string // what was scanned: absolute path, or host:/path
	host     string // the machine the tree describes
	imported bool   // not this machine's live filesystem: no local actions
	platform string
	elapsed  time.Duration
}

func localSource(cfg *config, opts scanner.ScanOptions, stderr io.Writer) (*source, error) {
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
		return nil, last.Err
	}
	if last.Root == nil {
		return nil, fmt.Errorf("%s : scan interrompu", abs)
	}
	h.save(last, stderr)
	host, _ := os.Hostname()
	return &source{res: last, disk: getDiskInfo(cfg.path), hist: h, label: last.Root.Path(), host: host, elapsed: last.Elapsed}, nil
}

// fromTree turns an imported tree into the same shape as a local scan.
func fromTree(root *scanner.Node, meta export.Meta) scanner.ScanProgress {
	since := time.Now().Add(-scanner.RecentWindow).Unix()
	if meta.Timestamp > 0 {
		since = meta.Timestamp - int64(scanner.RecentWindow/time.Second)
	}
	a := scanner.Analyze(root, since, 32)
	return scanner.ScanProgress{
		Files: int(root.FileCount), Dirs: int(root.DirCount), Size: root.Size, Done: true,
		Root: root, TopFiles: a.TopFiles, Recent: a.Recent, RecentSince: since,
		Errors: a.Errors, ErrorCount: a.ErrorCount,
	}
}

func metaDisk(m export.Meta) display.DiskInfo {
	return display.DiskInfo{Total: m.FSTotal, Used: m.FSUsed, Avail: m.FSAvail}
}

func importSource(cfg *config) (*source, error) {
	var r io.Reader = os.Stdin
	if cfg.importFile != "-" {
		f, err := os.Open(cfg.importFile)
		if err != nil {
			return nil, err
		}
		defer f.Close()
		r = f
	}
	root, meta, err := export.ReadNcdu(bufio.NewReaderSize(r, 1<<20))
	if err != nil {
		return nil, err
	}
	label := root.Path()
	if meta.Host != "" {
		label = meta.Host + ":" + label
	}
	// History is read (growth since the last scan of that host) but an
	// import is never recorded: it may be an old file.
	h := loadHistory(cfg, label)
	h.store = nil
	return &source{res: fromTree(root, meta), disk: metaDisk(meta), hist: h, label: label, host: meta.Host, imported: true}, nil
}

func remoteOptions(cfg *config, status func(string)) remote.Options {
	return remote.Options{
		SSHOpts: cfg.sshOpts, Sudo: cfg.sshSudo, Mode: cfg.mode, Excludes: cfg.excludes,
		Timeout: cfg.sshTimeout, Version: version, Binary: cfg.sshBinary, Status: status,
	}
}

func remoteSource(ctx context.Context, cfg *config, t remote.Target, status func(string)) (*source, error) {
	h := loadHistory(cfg, t.String())
	r, err := remote.Scan(ctx, t, remoteOptions(cfg, status))
	if err != nil {
		return nil, err
	}
	res := fromTree(r.Root, r.Meta)
	h.save(res, io.Discard)
	return &source{res: res, disk: metaDisk(r.Meta), hist: h, label: t.String(), host: t.Host, imported: true, platform: r.Platform, elapsed: r.Elapsed}, nil
}

// ── Outputs ──────────────────────────────────────────────────────────────────

func summaryData(src *source, cfg *config) display.SummaryData {
	res := src.res
	d := display.SummaryData{
		Root:       res.Root,
		TopFiles:   res.TopFiles,
		Disk:       src.disk,
		TopN:       cfg.topN,
		ErrorCount: res.ErrorCount,
		Errors:     res.Errors,
		Recent:     res.Recent[res.Root],
		Pinned:     res.Pinned,
	}
	if !src.imported {
		d.Annotate = summaryAnnotator()
	}
	if cfg.topN > len(res.TopFiles) {
		d.TopFiles = scanner.TopFiles(res.Root, cfg.topN)
	}
	if src.hist.prev != nil {
		d.Since = src.hist.prev.Time
		d.Growth = src.hist.prev.TopGrowth(res.Root, cfg.topN)
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

// writeAtomic writes a file through a temporary file in the same directory
// and a rename, so readers (node_exporter, cron jobs) never see half a file.
func writeAtomic(path string, write func(io.Writer) error) error {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, "."+filepath.Base(path)+".*.tmp")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	bw := bufio.NewWriterSize(tmp, 1<<20)
	if err := write(bw); err != nil {
		tmp.Close()
		return err
	}
	if err := bw.Flush(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Chmod(0o644); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}

func writeOutputs(stdout, stderr io.Writer, cfg *config, src *source) int {
	code := exitFor(src.res)
	fail := func(err error) int {
		fmt.Fprintf(stderr, "Erreur : %v\n", err)
		return exitFatal
	}

	if cfg.exportFile != "" {
		meta := export.Meta{Progver: version, Timestamp: time.Now().Unix(), Host: src.host,
			FSTotal: src.disk.Total, FSUsed: src.disk.Used, FSAvail: src.disk.Avail}
		write := func(w io.Writer) error { return export.WriteNcdu(w, src.res.Root, meta) }
		if cfg.exportFile == "-" {
			bw := bufio.NewWriterSize(stdout, 1<<20)
			if err := write(bw); err != nil {
				return fail(err)
			}
			if err := bw.Flush(); err != nil {
				return fail(err)
			}
		} else if err := writeAtomic(cfg.exportFile, write); err != nil {
			return fail(err)
		}
	}
	if cfg.promFile != "" {
		labels := map[string]string{}
		if src.host != "" {
			labels["host"] = src.host
		}
		opts := export.PromOptions{MaxDepth: cfg.promDepth, Labels: labels, Timestamp: time.Now().Unix()}
		if err := writeAtomic(cfg.promFile, func(w io.Writer) error { return export.WriteProm(w, src.res.Root, opts) }); err != nil {
			return fail(err)
		}
	}
	if cfg.doReport {
		path, err := writeReport(cfg, src)
		if err != nil {
			return fail(err)
		}
		fmt.Fprintf(stdout, "Rapport créé : %s\n", path)
	}

	switch {
	case cfg.doJSON:
		if err := display.SummaryJSON(stdout, summaryData(src, cfg)); err != nil {
			return fail(err)
		}
	case cfg.doTree:
		display.Tree(stdout, src.res.Root, cfg.treeDepth)
		if src.res.ErrorCount > 0 {
			fmt.Fprintf(stderr, "Avertissement : %d dossier(s) illisible(s), tailles sous-estimées\n", src.res.ErrorCount)
		}
	case cfg.doSummary:
		display.Summary(stdout, summaryData(src, cfg))
	case cfg.doReport || cfg.exportFile != "" || cfg.promFile != "":
		// file outputs only: stay quiet (cron friendly)
	default:
		display.Summary(stdout, summaryData(src, cfg))
	}
	return code
}

func writeReport(cfg *config, src *source) (string, error) {
	if err := os.MkdirAll(cfg.reportDir, 0o755); err != nil {
		return "", err
	}
	safeName := filepath.Base(src.res.Root.Path())
	if safeName == "" || safeName == "." || safeName == "/" {
		safeName = "root"
	}
	if src.imported && src.host != "" {
		safeName = src.host + "_" + safeName
	}
	ts := time.Now().Format("2006-01-02_15-04-05")
	reportPath := filepath.Join(cfg.reportDir, fmt.Sprintf("Report_%s_%s.txt", safeName, ts))
	err := writeAtomic(reportPath, func(w io.Writer) error {
		display.Summary(w, summaryData(src, cfg))
		return nil
	})
	return reportPath, err
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

// ── TUI ──────────────────────────────────────────────────────────────────────

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
	m := tui.Model{
		Path:        abs,
		Version:     version,
		State:       tui.StateScanning,
		ScannerChan: scanner.Scan(ctx, cfg.path, opts),
		CancelScan:  cancel,
		ScanOpts:    opts,
		ReadOnly:    cfg.readOnly,
		Hooks:       localHooks(),
		Prev:        h.prev,
		SaveSnapshot: func(res scanner.ScanProgress) {
			h.save(res, io.Discard)
		},
	}
	return runProgram(stderr, m, true)
}

// runTUIFromSource browses an imported tree: read-only, no local actions.
func runTUIFromSource(stderr io.Writer, cfg *config, src *source) int {
	ch := make(chan scanner.ScanProgress, 1)
	ch <- src.res
	close(ch)
	return runProgram(stderr, importedModel(cfg, src.label, src.hist.prev, ch, src.disk), false)
}

func importedModel(cfg *config, label string, prev *snapshot.Snapshot, ch <-chan scanner.ScanProgress, disk display.DiskInfo) tui.Model {
	return tui.Model{
		Path:        label,
		Version:     version,
		State:       tui.StateScanning,
		ScannerChan: ch,
		ScanOpts:    scanner.ScanOptions{SameDevice: cfg.mode == "partition"},
		Imported:    true,
		Source:      label,
		Disk:        [3]int64{disk.Total, disk.Used, disk.Avail},
		Prev:        prev,
	}
}

// runRemoteTUI shows the remote scan's progress, then browses the result.
func runRemoteTUI(stderr io.Writer, cfg *config, t remote.Target) int {
	ch := make(chan scanner.ScanProgress, 8)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	prev := loadHistory(cfg, t.String()).prev
	start := time.Now()
	var disk display.DiskInfo // written before the final send, read after it
	go func() {
		defer close(ch)
		status := func(s string) {
			select {
			case ch <- scanner.ScanProgress{Current: s, Elapsed: time.Since(start)}:
			default:
			}
		}
		src, err := remoteSource(ctx, cfg, t, status)
		if err != nil {
			ch <- scanner.ScanProgress{Done: true, Err: err}
			return
		}
		disk = src.disk
		ch <- src.res
	}()
	m := importedModel(cfg, t.String(), prev, ch, display.DiskInfo{})
	m.CancelScan = cancel
	m.DiskOnDone = func() [3]int64 { return [3]int64{disk.Total, disk.Used, disk.Avail} }
	return runProgram(stderr, m, false)
}

func runProgram(stderr io.Writer, m tui.Model, updates bool) int {
	if updates && updater.CheckEnabled() {
		updateCh := make(chan string, 1)
		go func() {
			if latest, ok := updater.UpdateAvailable(version); ok {
				updateCh <- latest
			}
		}()
		m.UpdateChan = updateCh
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
