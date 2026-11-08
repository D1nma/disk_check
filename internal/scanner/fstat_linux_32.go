//go:build linux && !(amd64 || arm64 || loong64 || mips64 || mips64le || ppc64 || ppc64le || riscv64 || s390x)

package scanner

import (
	"bytes"

	"golang.org/x/sys/unix"
)

// fstatatRaw for 32-bit Linux platforms without the generic newfstatat
// syscall: keep it simple and delegate to unix.Fstatat. These are not
// release targets; correctness over raw speed there.
func fstatatRaw(dirfd int, nameZ []byte, st *unix.Stat_t, flags int) error {
	name := nameZ
	if i := bytes.IndexByte(name, 0); i >= 0 {
		name = name[:i]
	}
	return unix.Fstatat(dirfd, string(name), st, flags)
}
