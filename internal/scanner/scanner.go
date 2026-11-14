package scanner

import (
	"container/heap"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"
)

// RecentWindow is how far back a file modification counts as "recent" for
// ScanProgress.Recent.
const RecentWindow = 24 * time.Hour

// maxErrors caps ScanProgress.Errors; ErrorCount keeps the true total.
const maxErrors = 1000

func workerCount(requested int) int {
	if requested > 0 {
		if requested > 64 {
			return 64
		}
		return requested
	}
	n := runtime.GOMAXPROCS(0) * 4
	if n > 32 {
		return 32
	}
	if n < 2 {
		return 2
	}
	return n
}

type dirTask struct {
	path      string
	node      *Node
	fsType    int64  // filesystem type of the parent, inherited when no mount is crossed
	parentDev uint64 // st_dev of the parent directory
}

// dirInfo is what listDir learns about the directory itself.
type dirInfo struct {
	size     int64  // the directory inode's own blocks
	fsType   int64  // statfs f_type (inherited when not re-stat-ed)
	subdirs  int    // number of subdirectories listed
	dev      uint64 // st_dev of the directory
	statErrs int    // entries that could not be stat-ed (vanished entries excluded)
	statErr  error  // first of those errors
}

// linkRef records a file whose inode has several links, for the post-scan
// attribution pass.
type linkRef struct {
	node  *Node
	dev   uint64
	ino   uint64
	nlink uint64
}

func joinPath(dir, name string) string {
	if dir == string(os.PathSeparator) {
		return dir + name
	}
	return dir + string(os.PathSeparator) + name
}

type nodeArena struct {
	cur []Node
	i   int
}

func (a *nodeArena) new() *Node {
	if a.i >= len(a.cur) {
		a.cur = make([]Node, 1024)
		a.i = 0
	}
	n := &a.cur[a.i]
	a.i++
	return n
}

// scanWorker bundles the per-worker arena, the reusable listing buffers and
// the results that are merged once the walk is over (no locks while walking).
type scanWorker struct {
	nodes     nodeArena
	listing   listCtx
	links     []linkRef
	errs      []ScanError
	mountDevs map[*Node]uint64
}

func (w *scanWorker) addError(path string, err error) {
	if len(w.errs) < maxErrors {
		w.errs = append(w.errs, ScanError{Path: path, Err: describeErr(err)})
	}
}

// describeErr turns common errno values into short French messages.
func describeErr(err error) string {
	var errno syscall.Errno
	if errors.As(err, &errno) {
		switch errno {
		case syscall.EACCES, syscall.EPERM:
			return "permission refusée"
		case syscall.ENOENT:
			return "introuvable"
		case syscall.EIO:
			return "erreur d'entrée/sortie"
		case syscall.ELOOP:
			return "trop de liens symboliques"
		case syscall.ENOTDIR:
			return "pas un dossier"
		case syscall.ESTALE:
			return "handle NFS périmé"
		}
	}
	return err.Error()
}

// Scan builds a full Node tree and reports progress via ScanProgress. The
// last message has Done set; it carries either Root (with the post-scan
// results) or Err when the root itself cannot be read.
func Scan(ctx context.Context, root string, opts ScanOptions) <-chan ScanProgress {
	ch := make(chan ScanProgress, 16)
	go func() {
		defer close(ch)
		scanStart := time.Now()

		sendFinal := func(p ScanProgress) {
			p.Done = true
			p.Elapsed = time.Since(scanStart)
			// Blocking send would leak this goroutine if the consumer quit after cancel.
			select {
			case ch <- p:
			case <-ctx.Done():
			}
		}

		abs, err := filepath.Abs(root)
		if err != nil {
			abs = root
		}
		rootInfo, err := os.Lstat(abs)
		if err != nil {
			sendFinal(ScanProgress{Current: abs, Err: fmt.Errorf("%s : %s", abs, describeErr(err))})
			return
		}

		var rootDev uint64
		if st, ok := rootInfo.Sys().(*syscall.Stat_t); ok {
			rootDev = uint64(st.Dev)
		}

		rootNode := &Node{
			Name:    abs,
			IsDir:   rootInfo.IsDir(),
			ModTime: rootInfo.ModTime().Unix(),
		}
		if !rootNode.IsDir {
			rootNode.Size = blockSize(rootInfo)
			rootNode.FileCount = 1
			sendFinal(ScanProgress{
				Files: 1, Size: rootNode.Size, Current: abs, Root: rootNode,
				DevBytes: map[uint64]int64{rootDev: rootNode.Size},
			})
			return
		}

		var totalFiles int64
		var totalDirs int64
		var totalSize int64
		var errCount atomic.Int64
		var current atomic.Value
		current.Store(abs)

		nWorkers := workerCount(opts.Workers)
		branchThreshold := nWorkers / 2
		if branchThreshold < 2 {
			branchThreshold = 2
		}
		// Mount roots are the only places where the filesystem type can
		// change; everywhere else it is inherited from the parent, saving
		// one fstatfs per directory. loadMounts()==nil (no mountinfo)
		// falls back to stat-ing every directory.
		mountTable := loadMountTable()
		var mounts map[string]struct{}
		if len(mountTable) > 0 {
			mounts = make(map[string]struct{}, len(mountTable))
			for _, m := range mountTable {
				mounts[m.path] = struct{}{}
			}
		}
		expected := expectedEntries(abs, opts.SameDevice, mountTable, opts.Excludes)
		if expected == 0 {
			expected = opts.ExpectedHint
		}

		taskCh := make(chan dirTask, nWorkers*4)
		var taskWg sync.WaitGroup
		var workerWg sync.WaitGroup
		var activeWorkers atomic.Int32
		var growWorkers func(target int)
		var workersMu sync.Mutex
		var allWorkers []*scanWorker
		var rootErr error

		var processDir func(path string, node *Node, isRoot bool, w *scanWorker, parentFs int64, parentDev uint64)
		processDir = func(path string, node *Node, isRoot bool, w *scanWorker, parentFs int64, parentDev uint64) {
			defer taskWg.Done()
			if ctx.Err() != nil {
				return
			}
			current.Store(path)

			// The filesystem type can only change at a mount root.
			wantStat := isRoot || parentFs == 0
			if !wantStat {
				if mounts == nil {
					wantStat = true
				} else if _, onMount := mounts[path]; onMount {
					wantStat = true
				}
			}
			ents, info, err := listDir(&w.listing, path, wantStat, parentFs, isRoot)
			if err != nil {
				if isRoot {
					rootErr = fmt.Errorf("%s : %s", path, describeErr(err))
					return
				}
				node.Flags |= FlagUnreadable
				node.Size = info.size
				node.OwnSize = info.size
				errCount.Add(1)
				w.addError(path, err)
				return
			}
			if !isRoot && isKernFS(info.fsType) {
				return
			}
			if isRoot || info.dev != parentDev {
				if !isRoot {
					node.Flags |= FlagMountRoot
				}
				if w.mountDevs == nil {
					w.mountDevs = make(map[*Node]uint64)
				}
				w.mountDevs[node] = info.dev
			}
			if info.statErrs > 0 {
				node.Flags |= FlagUnreadable
				errCount.Add(1)
				w.addError(path, fmt.Errorf("%d entrée(s) illisible(s) : %s", info.statErrs, describeErr(info.statErr)))
			}
			// Wide branches have enough parallel work to use the full pool now.
			if info.subdirs >= branchThreshold {
				growWorkers(nWorkers)
			}
			node.Size = info.size
			node.OwnSize = info.size

			localChildren := make([]*Node, 0, len(ents))
			var leftover []dirTask
			var localFiles, localDirs, localSize int64
			hasExcludes := len(opts.Excludes) > 0

			for _, ent := range ents {
				if ctx.Err() != nil {
					break
				}
				if opts.SameDevice && rootDev != 0 && ent.dev != rootDev {
					continue
				}

				var childPath string
				if ent.isDir || hasExcludes {
					childPath = joinPath(path, ent.name)
					if hasExcludes && isExcluded(childPath, opts.Excludes) {
						continue
					}
				}

				child := w.nodes.new()
				*child = Node{
					Name:    ent.name,
					IsDir:   ent.isDir,
					ModTime: ent.modTime,
					Parent:  node,
				}

				if !ent.isDir {
					child.Size = ent.size
					child.FileCount = 1
					if ent.nlink > 1 {
						// Attributed after the walk, deterministically.
						child.Flags |= FlagHardlink
						w.links = append(w.links, linkRef{node: child, dev: ent.dev, ino: ent.ino, nlink: ent.nlink})
					}
					localFiles++
					localSize += ent.size
				} else {
					localDirs++
					taskWg.Add(1)
					t := dirTask{path: childPath, node: child, fsType: info.fsType, parentDev: info.dev}
					select {
					case taskCh <- t:
						if pending := len(taskCh); pending > int(activeWorkers.Load()) {
							growWorkers(pending * 2)
						}
					default:
						leftover = append(leftover, t)
					}
				}
				localChildren = append(localChildren, child)
			}
			if localFiles != 0 {
				atomic.AddInt64(&totalFiles, localFiles)
			}
			if localDirs != 0 {
				atomic.AddInt64(&totalDirs, localDirs)
			}
			if localSize != 0 {
				atomic.AddInt64(&totalSize, localSize)
			}
			node.Children = localChildren
			for _, t := range leftover {
				processDir(t.path, t.node, false, w, t.fsType, t.parentDev)
			}
		}

		stopProgress := make(chan struct{})
		var progressWg sync.WaitGroup
		progressWg.Add(1)
		go func() {
			defer progressWg.Done()
			tick := time.NewTicker(100 * time.Millisecond)
			defer tick.Stop()
			for {
				select {
				case <-stopProgress:
					return
				case <-tick.C:
					cur, _ := current.Load().(string)
					p := ScanProgress{
						Files:      int(atomic.LoadInt64(&totalFiles)),
						Dirs:       int(atomic.LoadInt64(&totalDirs)),
						Size:       atomic.LoadInt64(&totalSize),
						Current:    cur,
						Expected:   expected,
						ErrorCount: int(errCount.Load()),
						Elapsed:    time.Since(scanStart),
					}
					select {
					case ch <- p:
					default:
					}
				}
			}
		}()

		spawnWorker := func() {
			workerWg.Add(1)
			w := &scanWorker{}
			workersMu.Lock()
			allWorkers = append(allWorkers, w)
			workersMu.Unlock()
			go func() {
				defer workerWg.Done()
				for t := range taskCh {
					processDir(t.path, t.node, false, w, t.fsType, t.parentDev)
				}
			}()
		}
		growWorkers = func(target int) {
			if target > nWorkers {
				target = nWorkers
			}
			for {
				workers := activeWorkers.Load()
				if int(workers) >= target {
					return
				}
				if activeWorkers.CompareAndSwap(workers, int32(target)) {
					for i := int(workers); i < target; i++ {
						spawnWorker()
					}
					return
				}
			}
		}
		activeWorkers.Store(1)
		spawnWorker()

		taskWg.Add(1)
		go func() {
			taskWg.Wait()
			close(taskCh)
		}()
		rootWorker := &scanWorker{}
		processDir(abs, rootNode, true, rootWorker, 0, rootDev)
		workerWg.Wait()
		close(stopProgress)
		progressWg.Wait()

		if rootErr != nil {
			sendFinal(ScanProgress{Current: abs, Err: rootErr})
			return
		}

		// Merge the per-worker results.
		workers := append(allWorkers, rootWorker)
		var links []linkRef
		var scanErrs []ScanError
		mountDevs := make(map[*Node]uint64)
		for _, w := range workers {
			links = append(links, w.links...)
			scanErrs = append(scanErrs, w.errs...)
			for n, d := range w.mountDevs {
				mountDevs[n] = d
			}
		}
		sort.Slice(scanErrs, func(i, j int) bool { return scanErrs[i].Path < scanErrs[j].Path })
		if len(scanErrs) > maxErrors {
			scanErrs = scanErrs[:maxErrors]
		}

		pinned := attributeHardlinks(links, rootNode)

		recentSince := scanStart.Add(-RecentWindow).Unix()
		res := aggregateTree(rootNode, rootDev, mountDevs, recentSince)

		cur, _ := current.Load().(string)
		sendFinal(ScanProgress{
			Files:       int(atomic.LoadInt64(&totalFiles)),
			Dirs:        int(atomic.LoadInt64(&totalDirs)),
			Size:        rootNode.Size,
			Current:     cur,
			Root:        rootNode,
			TopFiles:    res.topFiles,
			Expected:    expected,
			Errors:      scanErrs,
			ErrorCount:  int(errCount.Load()),
			DevBytes:    res.devBytes,
			Pinned:      pinned,
			Recent:      res.recent,
			RecentSince: recentSince,
		})
	}()
	return ch
}

// aggregateResult is what the post-scan pass computes besides the sizes
// stored in the nodes themselves.
type aggregateResult struct {
	topFiles []*Node
	devBytes map[uint64]int64
	recent   map[*Node]int64
}

// aggregateTree sums sizes and counts bottom-up, propagates FlagPartial,
// collects the top files, the bytes per filesystem and the recently
// modified bytes per directory.
func aggregateTree(root *Node, rootDev uint64, mountDevs map[*Node]uint64, recentSince int64) aggregateResult {
	const topCap = 32
	topH := &fileHeap{}
	heap.Init(topH)
	devBytes := make(map[uint64]int64)
	recent := make(map[*Node]int64)

	var aggregate func(n *Node, dev uint64) int64
	aggregate = func(n *Node, dev uint64) int64 {
		if !n.IsDir {
			if n.Size > 0 {
				devBytes[dev] += n.Size
				if topH.Len() < topCap {
					heap.Push(topH, n)
				} else if (*topH)[0].Size < n.Size {
					heap.Pop(topH)
					heap.Push(topH, n)
				}
				if n.ModTime >= recentSince {
					return n.Size
				}
			}
			return 0
		}
		if d, ok := mountDevs[n]; ok {
			dev = d
		}
		own := n.Size
		devBytes[dev] += own
		var files, dirs, sz, rec int64
		for _, child := range n.Children {
			rec += aggregate(child, dev)
			sz += child.Size
			if child.IsDir {
				files += int64(child.FileCount)
				dirs += 1 + int64(child.DirCount)
				if child.Incomplete() {
					n.Flags |= FlagPartial
				}
			} else {
				files++
			}
		}
		n.Size = own + sz
		n.FileCount = int32(files)
		n.DirCount = int32(dirs)
		if rec > 0 {
			recent[n] = rec
		}
		return rec
	}
	aggregate(root, rootDev)

	topFiles := make([]*Node, topH.Len())
	for i := len(topFiles) - 1; i >= 0; i-- {
		topFiles[i] = heap.Pop(topH).(*Node)
	}
	return aggregateResult{topFiles: topFiles, devBytes: devBytes, recent: recent}
}

// attributeHardlinks makes hard-link accounting deterministic: among the
// links of one inode found by the scan, the one with the smallest path keeps
// the size and the others get 0, whatever order the workers saw them in.
//
// It returns the pinned bytes per node: bytes included in a node's Size that
// deleting the node would not free, because another link to the same inode
// lives outside it (or outside the scan). Freeable(n) = n.Size - pinned[n].
func attributeHardlinks(links []linkRef, root *Node) map[*Node]int64 {
	pinned := make(map[*Node]int64)
	if len(links) == 0 {
		return pinned
	}
	sort.Slice(links, func(i, j int) bool {
		if links[i].dev != links[j].dev {
			return links[i].dev < links[j].dev
		}
		return links[i].ino < links[j].ino
	})
	var bufA, bufB []*Node
	for start := 0; start < len(links); {
		end := start + 1
		for end < len(links) && links[end].dev == links[start].dev && links[end].ino == links[start].ino {
			end++
		}
		group := links[start:end]
		start = end

		canon := group[0].node
		for _, l := range group[1:] {
			var c int
			c, bufA, bufB = comparePaths(l.node, canon, bufA, bufB)
			if c < 0 {
				canon = l.node
			}
		}
		size := canon.Size
		for _, l := range group {
			if l.node != canon {
				l.node.Size = 0
			}
		}
		if size == 0 {
			continue
		}

		// Deleting a directory frees the inode only if every link is inside it.
		var stop *Node // first ancestor (from canon upwards) that frees it; nil = none in the scan
		if uint64(len(group)) >= group[0].nlink {
			stop = lowestCommonAncestor(group)
		}
		for a := canon; a != nil && a != stop; a = a.Parent {
			pinned[a] += size
		}
	}
	return pinned
}

// Freeable returns how many bytes deleting n would actually free, given the
// Pinned map of the scan that produced n.
func Freeable(n *Node, pinned map[*Node]int64) int64 {
	if n == nil {
		return 0
	}
	f := n.Size - pinned[n]
	if f < 0 {
		return 0
	}
	return f
}

// chain returns n's ancestors from the root down to n, reusing buf.
func chain(n *Node, buf []*Node) []*Node {
	buf = buf[:0]
	for ; n != nil; n = n.Parent {
		buf = append(buf, n)
	}
	for i, j := 0, len(buf)-1; i < j; i, j = i+1, j-1 {
		buf[i], buf[j] = buf[j], buf[i]
	}
	return buf
}

// comparePaths orders two nodes of the same tree by path, component by
// component, without building path strings.
func comparePaths(a, b *Node, bufA, bufB []*Node) (int, []*Node, []*Node) {
	bufA = chain(a, bufA)
	bufB = chain(b, bufB)
	for i := 0; i < len(bufA) && i < len(bufB); i++ {
		if bufA[i] == bufB[i] {
			continue
		}
		if c := strings.Compare(bufA[i].Name, bufB[i].Name); c != 0 {
			return c, bufA, bufB
		}
	}
	switch {
	case len(bufA) < len(bufB):
		return -1, bufA, bufB
	case len(bufA) > len(bufB):
		return 1, bufA, bufB
	}
	return 0, bufA, bufB
}

// lowestCommonAncestor returns the deepest node that is an ancestor (or
// self) of every node in group.
func lowestCommonAncestor(group []linkRef) *Node {
	base := chain(group[0].node, nil)
	depth := len(base)
	var buf []*Node
	for _, l := range group[1:] {
		buf = chain(l.node, buf)
		i := 0
		for i < depth && i < len(buf) && base[i] == buf[i] {
			i++
		}
		depth = i
	}
	if depth == 0 {
		return nil
	}
	return base[depth-1]
}

func blockSize(info os.FileInfo) int64 {
	if st, ok := info.Sys().(*syscall.Stat_t); ok {
		return int64(st.Blocks) * 512
	}
	return info.Size()
}

func isExcluded(path string, excludes []string) bool {
	for _, ex := range excludes {
		if path == ex || strings.HasPrefix(path, ex+string(os.PathSeparator)) {
			return true
		}
	}
	return false
}
