//go:build !linux

package scanner

func isKernFS(fsType int64) bool { return false }
