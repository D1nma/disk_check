package tui

import (
	"context"
	"os"
	"sort"
	"strings"
	"syscall"
	"time"

	"github.com/D1nma/disk_check/internal/scanner"
	"github.com/D1nma/disk_check/internal/snapshot"
	"github.com/D1nma/disk_check/internal/updater"
	tea "github.com/charmbracelet/bubbletea"
)

// ── Sort ────────────────────────────────────────────────────────────────────

type SortKey int

const (
	SortSize SortKey = iota
	SortName
	SortDate
	SortGrowth
)

// ── Messages ─────────────────────────────────────────────────────────────────

// generation is embedded in scan messages to discard stale results after navigation.
type progressMsg struct {
	gen      int
	progress scanner.ScanProgress
}

type diskInfoMsg struct {
	path  string
	Total int64
	Used  int64
	Avail int64
}

type updateAvailableMsg struct{ tag string }

// updatePrefetchedMsg carries the result of the background download.
type updatePrefetchedMsg struct {
	tag  string
	file string
	err  error
}

type spinMsg struct{}

type fillMsg struct {
	path string
	text string
}

type activityMsg struct {
	gen   int
	path  string
	lines []string
}

type whyMsg struct {
	path  string
	lines []string
}

type deleteDoneMsg struct {
	nodes []*scanner.Node
	errs  []error
}

type rescanMsg struct {
	target   *scanner.Node
	progress scanner.ScanProgress
}



type updateState int

const (
	updateNone updateState = iota
	updateDownloading
	updateReady
	updateFailed
	updateDismissed
	updateNotice // newer release exists but this binary cannot replace itself
)

// ── Model ────────────────────────────────────────────────────────────────────

type State int

const (
	StateScanning State = iota
	StateBrowsing
)

// viewMode selects what the main area shows while browsing.
type viewMode int

const (
	viewList     viewMode = iota // the current directory
	viewTopFiles                 // largest files below the current directory
	viewErrors                   // unreadable paths
	viewMarks                    // marked entries: cleanup plan
	viewHelp
	viewWhy      // df vs scan
	viewActivity // live writers and growing files
)

// WhyInput is what the df-vs-scan explanation needs from the scan.
type WhyInput struct {
	ScanRoot   string
	SameDevice bool
	DevBytes   map[uint64]int64
	Unreadable int
}

// Hooks plug optional features implemented in other packages. Every hook
// may be nil; the corresponding key then says the feature is unavailable.
// Hooks run off the UI goroutine.
type Hooks struct {
	// Annotate describes a path (docker container, cache) and suggests the
	// proper cleanup command.
	Annotate func(absPath string, isDir bool) (label, cleanup string, ok bool)
	// Why explains the gap between df and the scan for the filesystem
	// holding path.
	Why func(path string, in WhyInput) []string
	// Fill samples the fill rate of the filesystem holding path and returns
	// a short header suffix ("" when idle or unknown). Called every 2 s.
	Fill func(path string) string
	// Activity lists the processes writing and the files growing on the
	// filesystem holding path. Called every 2 s while its panel is open.
	Activity func(path string) []string
}

type directoryView struct {
	sortBy      SortKey
	sortReverse bool
	maxSize     int64
}

type confirmState struct {
	nodes []*scanner.Node
	total int64
	free  int64
}

type Model struct {
	Path        string
	Version     string
	State       State
	Root        *scanner.Node
	Current     *scanner.Node
	Entries     []*scanner.Node
	Selected    int
	Offset      int // scroll offset
	Width       int
	Height      int
	ScannerChan <-chan scanner.ScanProgress
	UpdateChan  <-chan string
	CancelScan  context.CancelFunc
	SortBy      SortKey
	SortReverse bool
	ScanOpts    scanner.ScanOptions
	ReadOnly    bool
	Hooks       Hooks

	// Prev is the previous snapshot of this root (growth column); nil if none.
	Prev *snapshot.Snapshot
	// SaveSnapshot is called once, in the background, when the scan completes.
	SaveSnapshot func(scanner.ScanProgress)

	Progress scanner.ScanProgress

	// FatalErr is set when the scan could not start (root unreadable); the
	// TUI quits and main reports it.
	FatalErr error

	views       map[*scanner.Node]directoryView
	parentIndex map[*scanner.Node]int
	maxSize     int64

	// results of the scan
	pinned      map[*scanner.Node]int64
	recent      map[*scanner.Node]int64
	recentSince int64
	devBytes   map[uint64]int64
	scanErrs   []scanner.ScanError
	errorCount int

	// disk usage header (filesystem holding the current directory)
	diskPath  string
	diskTotal int64
	diskUsed  int64
	diskAvail int64
	fillText  string

	// update download state (TUI asks before installing)
	updateState updateState
	updateTag   string
	updateFile  string
	updateErr   string

	// set when the user accepted the update: main installs this file and
	// re-execs the binary after the TUI has fully exited.
	PendingUpdate string

	// generation counter — incremented on each navigation to discard stale messages
	generation int
	spin       int
	started    time.Time

	mode       viewMode
	overSel    int // selection in overlay lists
	overOff    int
	topFiles   map[*scanner.Node][]*scanner.Node
	whyLines   []string
	actLines   []string
	actGen     int
	marks      map[*scanner.Node]bool
	markOrder  []*scanner.Node
	confirm    *confirmState
	status     string
	statusErr  bool
	rescanning *scanner.Node
}

func (m Model) Init() tea.Cmd {
	cmds := []tea.Cmd{fetchDiskInfo(m.Path), spinTick()}
	if m.ScannerChan != nil {
		cmds = append(cmds, listenForProgress(m.ScannerChan, m.generation))
	}
	if m.UpdateChan != nil {
		cmds = append(cmds, listenForUpdate(m.UpdateChan))
	}
	if m.Hooks.Fill != nil {
		cmds = append(cmds, sampleFill(m.Hooks.Fill, m.Path, 0))
	}
	return tea.Batch(cmds...)
}

func (m Model) busy() bool {
	return m.State == StateScanning || m.rescanning != nil
}

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {

	case spinMsg:
		m.spin++
		if m.busy() {
			return m, spinTick()
		}
		return m, nil

	case updateAvailableMsg:
		m.updateTag = msg.tag
		if !updater.CanSelfUpdate() {
			m.updateState = updateNotice
			return m, nil
		}
		m.updateState = updateDownloading
		return m, prefetchUpdate(msg.tag)

	case updatePrefetchedMsg:
		if m.updateState == updateDismissed {
			if msg.file != "" {
				return m, removeFile(msg.file)
			}
			return m, nil
		}
		if msg.err != nil {
			m.updateState = updateFailed
			m.updateErr = msg.err.Error()
			return m, nil
		}
		m.updateState = updateReady
		m.updateFile = msg.file
		return m, nil

	case diskInfoMsg:
		if msg.path != "" && msg.path != m.Path {
			break // the user moved on
		}
		m.diskPath = msg.path
		m.diskTotal = msg.Total
		m.diskUsed = msg.Used
		m.diskAvail = msg.Avail

	case fillMsg:
		if msg.path == m.Path {
			m.fillText = msg.text
		}
		return m, sampleFill(m.Hooks.Fill, m.Path, 2*time.Second)

	case activityMsg:
		if m.mode != viewActivity || msg.gen != m.actGen {
			return m, nil // panel closed or reopened: this polling loop ends
		}
		if msg.path == m.Path {
			m.actLines = msg.lines
		}
		return m, pollActivity(m.Hooks.Activity, m.Path, m.actGen, 2*time.Second)

	case whyMsg:
		if msg.path == m.Path {
			m.whyLines = msg.lines
		}

	case deleteDoneMsg:
		return m.applyDeletes(msg), nil

	case rescanMsg:
		return m.handleRescan(msg)

	case progressMsg:
		if msg.gen != m.generation {
			break // stale — discard
		}
		m.Progress = msg.progress

		if msg.progress.Done {
			if msg.progress.Err != nil || msg.progress.Root == nil {
				m.FatalErr = msg.progress.Err
				if m.FatalErr == nil {
					m.FatalErr = errScanAborted
				}
				return m, tea.Quit
			}
			m.State = StateBrowsing
			m.Root = msg.progress.Root
			m.Current = msg.progress.Root
			m.pinned = msg.progress.Pinned
			m.recent = msg.progress.Recent
			m.recentSince = msg.progress.RecentSince
			m.topFiles = make(map[*scanner.Node][]*scanner.Node)
			m.devBytes = msg.progress.DevBytes
			m.scanErrs = msg.progress.Errors
			m.errorCount = msg.progress.ErrorCount
			m.Path = m.Current.Path()
			m.Entries = m.Current.Children
			m.sortEntries()
			var cmd tea.Cmd
			if m.SaveSnapshot != nil {
				save, res := m.SaveSnapshot, msg.progress
				cmd = func() tea.Msg { save(res); return nil }
			}
			return m, cmd
		}
		return m, listenForProgress(m.ScannerChan, m.generation)

	case tea.KeyMsg:
		return m.handleKey(msg)

	case tea.WindowSizeMsg:
		m.Width = msg.Width
		m.Height = msg.Height
		m.clampScroll()
	}
	return m, nil
}

type tuiError string

func (e tuiError) Error() string { return string(e) }

const errScanAborted = tuiError("scan interrompu")

func (m Model) handleKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	key := msg.String()
	m.status = ""
	m.statusErr = false

	if m.confirm != nil {
		switch key {
		case "o", "O", "y", "Y":
			c := m.confirm
			m.confirm = nil
			m.status = "Suppression en cours…"
			return m, deleteNodes(c.nodes)
		default:
			m.confirm = nil
			m.status = "Suppression annulée"
			return m, nil
		}
	}

	switch key {
	case "ctrl+c", "q":
		if m.CancelScan != nil {
			m.CancelScan()
		}
		return m, tea.Quit
	case "u":
		if m.updateState == updateReady {
			if m.CancelScan != nil {
				m.CancelScan()
			}
			m.PendingUpdate = m.updateFile
			return m, tea.Quit
		}
		return m, nil
	case "i":
		if m.updateState == updateDownloading || m.updateState == updateReady || m.updateState == updateNotice {
			file := m.updateFile
			m.updateState = updateDismissed
			m.updateFile = ""
			if file != "" {
				return m, removeFile(file)
			}
		}
		return m, nil
	}

	if m.State == StateScanning {
		return m, nil
	}

	if m.mode != viewList {
		return m.handleOverlayKey(key)
	}

	switch key {
	case "up", "k":
		m.move(-1)
	case "down", "j":
		m.move(1)
	case "pgup", "ctrl+b":
		m.move(-m.listHeight())
	case "pgdown", "ctrl+f", "ctrl+d":
		m.move(m.listHeight())
	case "home", "g":
		m.move(-len(m.Entries))
	case "end", "G":
		m.move(len(m.Entries))

	case "s":
		m.toggleSort(SortSize)
	case "n":
		m.toggleSort(SortName)
	case "t":
		m.toggleSort(SortDate)
	case "c":
		if m.Prev == nil {
			m.status = "Pas d'historique pour cette racine : la colonne Δ apparaîtra au prochain scan"
			return m, nil
		}
		m.toggleSort(SortGrowth)

	case "enter", "right", "l":
		if len(m.Entries) > 0 && m.Entries[m.Selected].IsDir {
			return m.enter(m.Entries[m.Selected], 0)
		}
	case "backspace", "left", "h":
		return m.leave()

	case " ":
		if len(m.Entries) > 0 {
			m.toggleMark(m.Entries[m.Selected])
			m.move(1)
		}
	case "m":
		m.openOverlay(viewMarks)
	case "d", "delete":
		if len(m.Entries) > 0 {
			m.askDelete([]*scanner.Node{m.Entries[m.Selected]})
		}
	case "f":
		m.openOverlay(viewTopFiles)
	case "e":
		if m.errorCount == 0 {
			m.status = "Aucun dossier illisible"
			return m, nil
		}
		m.openOverlay(viewErrors)
	case "?":
		m.openOverlay(viewHelp)
	case "w":
		if m.Hooks.Why == nil {
			m.status = "Explication df/scan indisponible sur ce système"
			return m, nil
		}
		m.openOverlay(viewWhy)
		m.whyLines = nil
		return m, explainWhy(m.Hooks.Why, m.Path, m.whyInput())
	case "a":
		if m.Hooks.Activity == nil {
			m.status = "Activité en direct indisponible sur ce système"
			return m, nil
		}
		m.openOverlay(viewActivity)
		m.actLines = nil
		m.actGen++
		return m, pollActivity(m.Hooks.Activity, m.Path, m.actGen, 0)
	case "y":
		if len(m.Entries) > 0 {
			return m, m.copyPath(m.Entries[m.Selected].Path())
		}
	case "r":
		return m.startRescan(m.Current)
	}
	return m, nil
}

func (m Model) whyInput() WhyInput {
	root := ""
	if m.Root != nil {
		root = m.Root.Path()
	}
	return WhyInput{ScanRoot: root, SameDevice: m.ScanOpts.SameDevice, DevBytes: m.devBytes, Unreadable: m.errorCount}
}

func (m *Model) openOverlay(v viewMode) {
	m.mode = v
	m.overSel = 0
	m.overOff = 0
}

func (m Model) handleOverlayKey(key string) (tea.Model, tea.Cmd) {
	items := m.overlayLen()
	switch key {
	case "esc", "backspace", "left", "h":
		m.mode = viewList
		return m, nil
	case "?":
		if m.mode == viewHelp {
			m.mode = viewList
		}
		return m, nil
	case "up", "k":
		m.overMove(-1, items)
	case "down", "j":
		m.overMove(1, items)
	case "pgup", "ctrl+b":
		m.overMove(-m.listHeight(), items)
	case "pgdown", "ctrl+f", "ctrl+d":
		m.overMove(m.listHeight(), items)
	case "home", "g":
		m.overMove(-items, items)
	case "end", "G":
		m.overMove(items, items)
	}

	switch m.mode {
	case viewTopFiles:
		files := m.currentTopFiles()
		if len(files) == 0 {
			return m, nil
		}
		f := files[m.overSel]
		switch key {
		case "enter", "right", "l":
			m.mode = viewList
			return m.reveal(f)
		case " ":
			m.toggleMark(f)
			m.overMove(1, items)
		case "d", "delete":
			m.askDelete([]*scanner.Node{f})
		case "y":
			return m, m.copyPath(f.Path())
		case "f":
			m.mode = viewList
		}
	case viewErrors:
		if len(m.scanErrs) == 0 {
			return m, nil
		}
		switch key {
		case "enter", "right", "l":
			if n := m.findPath(m.scanErrs[m.overSel].Path); n != nil {
				m.mode = viewList
				return m.reveal(n)
			}
			m.status = "Ce chemin n'est plus dans l'arbre"
		case "y":
			return m, m.copyPath(m.scanErrs[m.overSel].Path)
		case "e":
			m.mode = viewList
		}
	case viewMarks:
		marked := m.markedRoots()
		switch key {
		case "m":
			m.mode = viewList
		case "x":
			if len(marked) > 0 {
				path, err := m.writePlan()
				if err != nil {
					m.status, m.statusErr = "Plan non écrit : "+err.Error(), true
				} else {
					m.status = "Plan de nettoyage écrit : " + path + " (relisez-le avant de l'exécuter)"
				}
			}
		case "D":
			if len(marked) > 0 {
				m.askDelete(marked)
			}
		case " ", "delete":
			if len(marked) > 0 {
				m.toggleMark(marked[m.overSel])
				if m.overSel >= len(m.markedRoots()) && m.overSel > 0 {
					m.overSel--
				}
			}
		case "enter", "right", "l":
			if len(marked) > 0 {
				m.mode = viewList
				return m.reveal(marked[m.overSel])
			}
		case "y":
			if len(marked) > 0 {
				return m, m.copyPath(marked[m.overSel].Path())
			}
		}
	case viewWhy:
		if key == "w" {
			m.mode = viewList
		}
	case viewActivity:
		if key == "a" {
			m.mode = viewList
		}
	}
	return m, nil
}

func (m Model) overlayLen() int {
	switch m.mode {
	case viewTopFiles:
		return len(m.currentTopFiles())
	case viewErrors:
		return len(m.scanErrs)
	case viewMarks:
		return len(m.markedRoots())
	case viewWhy:
		return len(m.whyLines)
	case viewActivity:
		return len(m.actLines)
	case viewHelp:
		return len(helpLines)
	}
	return 0
}

func (m *Model) overMove(delta, items int) {
	m.overSel += delta
	if m.overSel >= items {
		m.overSel = items - 1
	}
	if m.overSel < 0 {
		m.overSel = 0
	}
	visible := m.listHeight()
	if m.overSel < m.overOff {
		m.overOff = m.overSel
	} else if m.overSel >= m.overOff+visible {
		m.overOff = m.overSel - visible + 1
	}
}

func (m *Model) move(delta int) {
	m.Selected += delta
	if m.Selected >= len(m.Entries) {
		m.Selected = len(m.Entries) - 1
	}
	if m.Selected < 0 {
		m.Selected = 0
	}
	m.clampScroll()
}

func (m *Model) toggleSort(k SortKey) {
	if m.SortBy == k {
		m.SortReverse = !m.SortReverse
	} else {
		m.SortBy = k
		m.SortReverse = false
	}
	m.sortEntries()
}

// enter opens dir and selects index sel.
func (m Model) enter(dir *scanner.Node, sel int) (tea.Model, tea.Cmd) {
	if m.parentIndex == nil {
		m.parentIndex = make(map[*scanner.Node]int)
	}
	if dir.Parent == m.Current {
		m.parentIndex[dir] = m.Selected
	}
	m.generation++
	m.Current = dir
	m.Entries = m.Current.Children
	m.restoreEntries()
	m.Path = m.Current.Path()
	m.Selected = sel
	m.Offset = 0
	m.clampScroll()
	return m, m.onDirChange()
}

func (m Model) leave() (tea.Model, tea.Cmd) {
	if m.Current == nil || m.Current.Parent == nil {
		return m, nil
	}
	oldNode := m.Current
	m.generation++
	m.Current = m.Current.Parent
	m.Entries = m.Current.Children
	cachedOrder := m.restoreEntries()
	m.Path = m.Current.Path()

	// Restore selection to the directory we just left
	m.Selected = 0
	if index, ok := m.parentIndex[oldNode]; cachedOrder && ok && index < len(m.Entries) && m.Entries[index] == oldNode {
		m.Selected = index
	} else {
		for i, e := range m.Entries {
			if e == oldNode {
				m.Selected = i
				break
			}
		}
	}
	m.clampScroll()
	return m, m.onDirChange()
}

// reveal navigates to n's parent directory and selects n.
func (m Model) reveal(n *scanner.Node) (tea.Model, tea.Cmd) {
	dir := n.Parent
	if dir == nil {
		dir = n
	}
	if dir != m.Current {
		model, cmd := m.enter(dir, 0)
		m = model.(Model)
		for i, e := range m.Entries {
			if e == n {
				m.Selected = i
				break
			}
		}
		m.clampScroll()
		return m, cmd
	}
	for i, e := range m.Entries {
		if e == n {
			m.Selected = i
			break
		}
	}
	m.clampScroll()
	return m, nil
}

// onDirChange refreshes what depends on the filesystem of the current directory.
func (m Model) onDirChange() tea.Cmd {
	if m.Current == nil {
		return nil
	}
	// The filesystem can only change at a mount root; statfs is one syscall,
	// so refreshing on every move keeps "% of disk" right across mounts.
	return fetchDiskInfo(m.Path)
}

// findPath returns the node for an absolute path, or nil.
func (m Model) findPath(path string) *scanner.Node {
	if m.Root == nil {
		return nil
	}
	base := m.Root.Path()
	if path == base {
		return m.Root
	}
	rel := strings.TrimPrefix(path, strings.TrimSuffix(base, "/")+"/")
	if rel == path {
		return nil
	}
	n := m.Root
	for _, part := range strings.Split(rel, "/") {
		var next *scanner.Node
		for _, c := range n.Children {
			if c.Name == part {
				next = c
				break
			}
		}
		if next == nil {
			return nil
		}
		n = next
	}
	return n
}

// ── Helpers ──────────────────────────────────────────────────────────────────

func (m *Model) clampScroll() {
	visible := m.listHeight()
	if m.Selected >= len(m.Entries) {
		m.Selected = len(m.Entries) - 1
	}
	if m.Selected < 0 {
		m.Selected = 0
	}
	if m.Selected < m.Offset {
		m.Offset = m.Selected
	} else if m.Selected >= m.Offset+visible {
		m.Offset = m.Selected - visible + 1
	}
	if m.Offset < 0 {
		m.Offset = 0
	}
}

func (m *Model) sortEntries() {
	var maxSize int64
	for _, e := range m.Entries {
		if e.Size > maxSize {
			maxSize = e.Size
		}
	}
	m.maxSize = maxSize
	var deltas map[*scanner.Node]int64
	if m.SortBy == SortGrowth && m.Prev != nil {
		deltas = make(map[*scanner.Node]int64, len(m.Entries))
		for _, e := range m.Entries {
			deltas[e], _ = m.Prev.Delta(e)
		}
	}
	sort.Slice(m.Entries, func(i, j int) bool {
		a, b := m.Entries[i], m.Entries[j]
		var cmp int
		switch m.SortBy {
		case SortSize:
			cmp = compareInt64(a.Size, b.Size)
		case SortName:
			cmp = strings.Compare(b.Name, a.Name)
		case SortDate:
			cmp = compareInt64(a.ModTime, b.ModTime)
		case SortGrowth:
			cmp = compareInt64(deltas[a], deltas[b])
		}
		if cmp == 0 {
			cmp = strings.Compare(b.Name, a.Name)
		}
		if m.SortReverse {
			return cmp < 0
		}
		return cmp > 0
	})
	if m.Current != nil {
		if m.views == nil {
			m.views = make(map[*scanner.Node]directoryView)
		}
		m.views[m.Current] = directoryView{m.SortBy, m.SortReverse, maxSize}
	}
}

func compareInt64(a, b int64) int {
	switch {
	case a < b:
		return -1
	case a > b:
		return 1
	default:
		return 0
	}
}

func (m *Model) restoreEntries() bool {
	if view, ok := m.views[m.Current]; ok && view.sortBy == m.SortBy && view.sortReverse == m.SortReverse {
		m.maxSize = view.maxSize
		return true
	}
	m.sortEntries()
	return false
}

// ── Commands ─────────────────────────────────────────────────────────────────

func spinTick() tea.Cmd {
	return tea.Tick(100*time.Millisecond, func(time.Time) tea.Msg { return spinMsg{} })
}

func listenForUpdate(ch <-chan string) tea.Cmd {
	return func() tea.Msg {
		tag, ok := <-ch
		if !ok || tag == "" {
			return nil
		}
		return updateAvailableMsg{tag: tag}
	}
}

// prefetchUpdate downloads and verifies the release binary in the
// background so the upgrade is instant once the user accepts it.
func prefetchUpdate(tag string) tea.Cmd {
	return func() tea.Msg {
		file, err := updater.Prefetch(tag)
		return updatePrefetchedMsg{tag: tag, file: file, err: err}
	}
}

// removeFile cleans up a prefetched update the user declined.
func removeFile(path string) tea.Cmd {
	return func() tea.Msg {
		os.Remove(path)
		return nil
	}
}

func listenForProgress(ch <-chan scanner.ScanProgress, gen int) tea.Cmd {
	return func() tea.Msg {
		p, ok := <-ch
		if !ok {
			return nil
		}
		return progressMsg{gen: gen, progress: p}
	}
}

func fetchDiskInfo(path string) tea.Cmd {
	return func() tea.Msg {
		var st syscall.Statfs_t
		if err := syscall.Statfs(path, &st); err != nil {
			return diskInfoMsg{path: path}
		}
		bs := int64(st.Bsize)
		return diskInfoMsg{
			path:  path,
			Total: int64(st.Blocks) * bs,
			Used:  (int64(st.Blocks) - int64(st.Bfree)) * bs,
			Avail: int64(st.Bavail) * bs,
		}
	}
}

func sampleFill(fill func(string) string, path string, after time.Duration) tea.Cmd {
	if fill == nil {
		return nil
	}
	run := func() tea.Msg { return fillMsg{path: path, text: fill(path)} }
	if after == 0 {
		return run
	}
	return tea.Tick(after, func(time.Time) tea.Msg { return run() })
}

func pollActivity(act func(string) []string, path string, gen int, after time.Duration) tea.Cmd {
	if act == nil {
		return nil
	}
	run := func() tea.Msg { return activityMsg{gen: gen, path: path, lines: act(path)} }
	if after == 0 {
		return run
	}
	return tea.Tick(after, func(time.Time) tea.Msg { return run() })
}

func explainWhy(why func(string, WhyInput) []string, path string, in WhyInput) tea.Cmd {
	return func() tea.Msg { return whyMsg{path: path, lines: why(path, in)} }
}
