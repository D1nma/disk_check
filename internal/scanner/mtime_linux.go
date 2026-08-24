//go:build linux

package scanner

import "golang.org/x/sys/unix"

func mtimeSec(st *unix.Stat_t) int64 {
	sec, _ := st.Mtim.Unix()
	return sec
}
