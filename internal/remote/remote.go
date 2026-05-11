package remote

import (
	"fmt"
	"github.com/D1nma/disk_check/internal/assets"
)

func RunRemote(host string) {
	fmt.Printf("Running remote on %s using embedded script (length: %d)\n", host, len(assets.BashScript))
}
