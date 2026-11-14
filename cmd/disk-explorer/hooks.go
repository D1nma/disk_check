package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/D1nma/disk_check/internal/annotate"
	"github.com/D1nma/disk_check/internal/display"
	"github.com/D1nma/disk_check/internal/live"
	"github.com/D1nma/disk_check/internal/reconcile"
	"github.com/D1nma/disk_check/internal/textsafe"
	"github.com/D1nma/disk_check/internal/tui"
)

// minShownRate is the fill rate under which the header stays quiet.
const minShownRate = 64 << 10

func devOf(path string) (uint64, bool) {
	var st syscall.Stat_t
	if err := syscall.Stat(path, &st); err != nil {
		return 0, false
	}
	return uint64(st.Dev), true
}

// localHooks wires the optional TUI features that inspect this machine.
// They must not be used for a tree imported from another host.
func localHooks() tui.Hooks {
	ann := annotate.New(annotate.Options{})

	var fillMu sync.Mutex
	meters := map[uint64]*live.FillMeter{}
	fillFor := func(path string) (live.Fill, bool) {
		dev, ok := devOf(path)
		if !ok {
			return live.Fill{}, false
		}
		fillMu.Lock()
		defer fillMu.Unlock()
		m := meters[dev]
		if m == nil {
			m = live.NewFillMeter(path, live.DefaultWindow)
			meters[dev] = m
		}
		f, err := m.Sample(time.Now())
		return f, err == nil
	}

	var actMu sync.Mutex
	trackers := map[uint64]*live.Tracker{}

	return tui.Hooks{
		Why: func(path string, in tui.WhyInput) []string {
			r, err := explainGap(path, in.ScanRoot, in.SameDevice, in.DevBytes, in.Unreadable)
			if err != nil {
				return []string{"  " + textsafe.Clean(err.Error())}
			}
			return reconcile.Lines(r, 15)
		},
		Annotate: func(path string, isDir bool) (string, string, bool) {
			a, ok := ann.Lookup(path, isDir)
			return a.Label, a.Cleanup, ok
		},
		Fill: func(path string) string {
			f, ok := fillFor(path)
			if !ok {
				return ""
			}
			return fillText(f)
		},
		Activity: func(path string) []string {
			dev, ok := devOf(path)
			if !ok {
				return []string{"  impossible de lire " + textsafe.Clean(path)}
			}
			actMu.Lock()
			defer actMu.Unlock()
			t := trackers[dev]
			if t == nil {
				t = live.NewTracker("/proc", dev)
				trackers[dev] = t
			}
			st, err := t.Poll(time.Now())
			var lines []string
			if f, ok := fillFor(path); ok {
				if s := fillText(f); s != "" {
					lines = append(lines, "  Remplissage : "+s, "")
				}
			}
			return append(lines, activityLines(st, err)...)
		},
	}
}

// explainGap builds the df-vs-scan report for the filesystem holding path,
// from a scan rooted at scanRoot.
func explainGap(path, scanRoot string, sameDevice bool, devBytes map[uint64]int64, unreadable int) (reconcile.Report, error) {
	fs, err := reconcile.Stat(path)
	if err != nil {
		return reconcile.Report{}, fmt.Errorf("impossible d'identifier le système de fichiers de %s : %v", path, err)
	}
	deleted, hidden, err := reconcile.DeletedOpen("/proc", fs.Dev)
	if err != nil && !errors.Is(err, errors.ErrUnsupported) {
		return reconcile.Report{}, err
	}
	return reconcile.Build(fs, devBytes[fs.Dev], scanCovers(scanRoot, fs, sameDevice), unreadable, deleted, hidden), nil
}

// scanCovers reports whether a scan rooted at scanRoot visited the whole
// filesystem fs: it started at the mount point, or above it without
// staying on one device.
func scanCovers(scanRoot string, fs reconcile.FSInfo, sameDevice bool) bool {
	if reconcile.StartsAtMount(scanRoot, fs) {
		return true
	}
	if sameDevice || fs.MountPoint == "" {
		return false
	}
	root := filepath.Clean(scanRoot)
	mp := filepath.Clean(fs.MountPoint)
	return root == "/" || strings.HasPrefix(mp, root+"/")
}

func fillText(f live.Fill) string {
	switch {
	case !f.Valid:
		return ""
	case f.RateBps >= minShownRate:
		s := "▲ +" + live.FormatRate(f.RateBps)
		if f.ETA > 0 {
			s += " → plein dans " + live.FormatETA(f.ETA)
		} else if f.Avail <= 0 {
			s += " → PLEIN"
		}
		return s
	case f.RateBps <= -minShownRate:
		return "▼ libère " + live.FormatRate(-f.RateBps)
	}
	return ""
}

func activityLines(st live.Status, err error) []string {
	if errors.Is(err, live.ErrUnsupported) {
		return []string{"  Activité par processus indisponible sur ce système (Linux uniquement)."}
	}
	if err != nil {
		return []string{"  Erreur : " + textsafe.Clean(err.Error())}
	}
	if st.Warmup {
		return []string{"  Première mesure… les débits apparaissent au relevé suivant (2 s), les fichiers au troisième."}
	}
	var lines []string
	lines = append(lines, "  FICHIERS QUI GROSSISSENT SUR CE SYSTÈME DE FICHIERS")
	if len(st.Files) == 0 {
		lines = append(lines, "    (aucun parmi les fichiers ouverts par les plus gros écrivains)")
	}
	for _, f := range st.Files {
		del := ""
		if f.Deleted {
			del = "  [SUPPRIMÉ mais ouvert : l'espace ne sera libéré qu'à la fermeture]"
		}
		lines = append(lines, fmt.Sprintf("    %12s  %10s  %s — %s (pid %d, fd %d)%s",
			"+"+live.FormatRate(f.GrowthBps), display.FormatSize(f.Size), textsafe.Clean(f.Path),
			textsafe.Clean(f.Comm), f.PID, f.FD, del))
	}
	lines = append(lines, "", "  PROCESSUS QUI ÉCRIVENT (tous systèmes de fichiers confondus, indicatif)")
	if len(st.Writers) == 0 {
		lines = append(lines, "    (aucune écriture mesurée)")
	}
	for _, w := range st.Writers {
		lines = append(lines, fmt.Sprintf("    %12s  %s (pid %d)", live.FormatRate(w.WriteBps), textsafe.Clean(w.Comm), w.PID))
	}
	if st.Hidden > 0 {
		hint := "relancez en root pour une vue complète"
		if os.Geteuid() == 0 {
			hint = "autre espace de noms ou restrictions du noyau"
		}
		lines = append(lines, "", fmt.Sprintf("  %d processus non inspectés : %s.", st.Hidden, hint))
	}
	return lines
}
