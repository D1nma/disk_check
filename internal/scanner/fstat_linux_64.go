//go:build linux && (amd64 || arm64 || loong64 || mips64 || mips64le || ppc64 || ppc64le || riscv64 || s390x)

package scanner

import (
	"unsafe"

	"golang.org/x/sys/unix"
)

// fstatatRaw stats nameZ (a NUL-terminated byte string, typically pointing
// straight into the getdents buffer) without allocating. 64-bit Linux
// exposes the generic newfstatat syscall directly.
func fstatatRaw(dirfd int, nameZ []byte, st *unix.Stat_t, flags int) error {
	_, _, errno := unix.Syscall6(unix.SYS_NEWFSTATAT, uintptr(dirfd),
		uintptr(unsafe.Pointer(&nameZ[0])), uintptr(unsafe.Pointer(st)),
		uintptr(flags), 0, 0)
	if errno != 0 {
		return errno
	}
	return nil
}
