//go:build darwin

package scanner

import "golang.org/x/sys/unix"

func mtimeSec(st *unix.Stat_t) int64 {
	sec, _ := st.Mtimespec.Unix()
	return sec
}
