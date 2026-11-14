package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strings"
	"sync"
	"text/tabwriter"

	"github.com/D1nma/disk_check/internal/display"
	"github.com/D1nma/disk_check/internal/remote"
	"github.com/D1nma/disk_check/internal/scanner"
	"github.com/D1nma/disk_check/internal/textsafe"
)

type fleetRow struct {
	target remote.Target
	src    *source
	err    error
}

// runFleet scans several hosts in parallel (--ssh-jobs at a time) and
// prints one line per host, then the main growths since the last run.
func runFleet(stdout, stderr io.Writer, cfg *config) int {
	if cfg.exportFile != "" || cfg.promFile != "" {
		fmt.Fprintln(stderr, "Erreur : --export et --prom ne prennent qu'un seul hôte à la fois")
		return exitUsage
	}
	rows := make([]fleetRow, len(cfg.targets))
	for i, s := range cfg.targets {
		t, err := targetFor(cfg, s)
		if err != nil {
			fmt.Fprintf(stderr, "Erreur : %v\n", err)
			return exitUsage
		}
		rows[i].target = t
	}

	var mu sync.Mutex
	status := func(s string) {
		mu.Lock()
		defer mu.Unlock()
		fmt.Fprintln(stderr, textsafe.Clean(s))
	}
	sem := make(chan struct{}, cfg.sshJobs)
	var wg sync.WaitGroup
	for i := range rows {
		wg.Add(1)
		go func(r *fleetRow) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			r.src, r.err = remoteSource(context.Background(), cfg, r.target, status)
			if r.err != nil {
				status("✗ " + r.err.Error())
			} else {
				status(fmt.Sprintf("✓ %s : %s en %.1f s", r.target, display.FormatSize(r.src.res.Root.Size), r.src.elapsed.Seconds()))
			}
		}(&rows[i])
	}
	wg.Wait()

	code := exitOK
	for _, r := range rows {
		switch {
		case r.err != nil:
			code = exitFatal
		case r.src.res.ErrorCount > 0 && code == exitOK:
			code = exitPartial
		}
	}

	if cfg.doReport {
		for _, r := range rows {
			if r.err != nil {
				continue
			}
			path, err := writeReport(cfg, r.src)
			if err != nil {
				fmt.Fprintf(stderr, "Erreur : %v\n", err)
				code = exitFatal
				continue
			}
			fmt.Fprintf(stdout, "Rapport créé : %s\n", path)
		}
	}
	if cfg.doJSON {
		if err := fleetJSON(stdout, rows, cfg); err != nil {
			fmt.Fprintf(stderr, "Erreur : %v\n", err)
			return exitFatal
		}
		return code
	}
	fleetTable(stdout, rows)
	return code
}

func fleetTable(w io.Writer, rows []fleetRow) {
	tw := tabwriter.NewWriter(w, 2, 4, 2, ' ', 0)
	fmt.Fprintln(tw, "HÔTE\tPLATEFORME\tTAILLE\tDISQUE\tΔ DERNIER SCAN\tÉTAT\tPLUS GROS DOSSIER")
	for _, r := range rows {
		if r.err != nil {
			fmt.Fprintf(tw, "%s\t—\t—\t—\t—\tÉCHEC\t%s\n", r.target, textsafe.Clean(shortErr(r.err, r.target.Host)))
			continue
		}
		res, root := r.src.res, r.src.res.Root
		disk := "—"
		if d := r.src.disk; d.Total > 0 {
			disk = fmt.Sprintf("%d%% (%s libres)", d.Used*100/d.Total, display.FormatSize(d.Avail))
		}
		delta := "—"
		if p := r.src.hist.prev; p != nil {
			delta = signed(root.Size - p.Size)
		}
		state := "OK"
		if res.ErrorCount > 0 {
			state = fmt.Sprintf("partiel (%d illisibles)", res.ErrorCount)
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\t%s\n", r.target, r.src.platform, display.FormatSize(root.Size),
			disk, delta, state, biggestChild(root))
	}
	tw.Flush()

	for _, r := range rows {
		if r.err != nil || r.src.hist.prev == nil {
			continue
		}
		growth := r.src.hist.prev.TopGrowth(r.src.res.Root, 3)
		if len(growth) == 0 {
			continue
		}
		fmt.Fprintf(w, "\n%s — croissance depuis le %s :\n", r.target, r.src.hist.prev.Time.Format("2006-01-02 15:04"))
		for _, g := range growth {
			fmt.Fprintf(w, "  %10s  %s\n", "+"+display.FormatSize(g.Delta), textsafe.Clean(g.Path))
		}
	}
}

func shortErr(err error, host string) string {
	return strings.TrimPrefix(err.Error(), host+" : ")
}

func signed(d int64) string {
	switch {
	case d > 0:
		return "+" + display.FormatSize(d)
	case d < 0:
		return "-" + display.FormatSize(-d)
	}
	return "±0"
}

func biggestChild(root *scanner.Node) string {
	var dirs []*scanner.Node
	for _, c := range root.Children {
		if c.IsDir {
			dirs = append(dirs, c)
		}
	}
	if len(dirs) == 0 {
		return "—"
	}
	sort.Slice(dirs, func(i, j int) bool { return dirs[i].Size > dirs[j].Size })
	return textsafe.Clean(dirs[0].Name) + "/ " + display.FormatSize(dirs[0].Size)
}

func fleetJSON(w io.Writer, rows []fleetRow, cfg *config) error {
	type hostJSON struct {
		Target   string          `json:"target"`
		Platform string          `json:"platform,omitempty"`
		Error    string          `json:"error,omitempty"`
		Summary  json.RawMessage `json:"summary,omitempty"`
	}
	out := make([]hostJSON, 0, len(rows))
	for _, r := range rows {
		h := hostJSON{Target: r.target.String()}
		if r.err != nil {
			h.Error = r.err.Error()
		} else {
			h.Platform = r.src.platform
			var buf bytes.Buffer
			if err := display.SummaryJSON(&buf, summaryData(r.src, cfg)); err != nil {
				return err
			}
			h.Summary = json.RawMessage(bytes.TrimSpace(buf.Bytes()))
		}
		out = append(out, h)
	}
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(out)
}
