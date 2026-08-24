package display

import (
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/D1nma/disk_check/internal/scanner"
)

type DiskInfo struct {
	Total int64
	Used  int64
	Avail int64
}

func Summary(w io.Writer, path string, root *scanner.Node, entries []*scanner.Node, topFiles []*scanner.Node, di DiskInfo, topN int) {
	fmt.Fprintf(w, "RAPPORT DISQUE - %s\n", time.Now().Format("2006-01-02 15:04:05"))
	fmt.Fprintf(w, "Dossier : %s\n", path)
	if root != nil {
		fmt.Fprintf(w, "Taille  : %s  (%d fichiers, %d dossiers)\n",
			FormatSize(root.Size), root.FileCount, root.DirCount)
	}
	if di.Total > 0 {
		pct := di.Used * 100 / di.Total
		fmt.Fprintf(w, "Disque  : %s utilisé / %s total (%d%%)\n",
			FormatSize(di.Used), FormatSize(di.Total), pct)
		fmt.Fprintf(w, "Libre   : %s\n", FormatSize(di.Avail))
		if root != nil {
			fmt.Fprintf(w, "Part de ce dossier dans le disque : %d%%\n",
				root.Size*100/di.Total)
		}
	}
	fmt.Fprintln(w)

	fmt.Fprintf(w, "TOP SOUS-DOSSIERS :\n")
	count := 0
	for _, e := range entries {
		if !e.IsDir {
			continue
		}
		if count >= topN {
			break
		}
		fmt.Fprintf(w, "  %10s  %s/\n", FormatSize(e.Size), e.Name)
		count++
	}
	if count == 0 {
		fmt.Fprintln(w, "  (aucun)")
	}
	fmt.Fprintln(w)

	fmt.Fprintf(w, "TOP FICHIERS :\n")
	for _, f := range topFiles {
		rel := f.Path()
		if strings.HasPrefix(rel, path+"/") {
			rel = rel[len(path)+1:]
		}
		fmt.Fprintf(w, "  %10s  %s\n", FormatSize(f.Size), rel)
	}
	if len(topFiles) == 0 {
		fmt.Fprintln(w, "  (aucun)")
	}
}
