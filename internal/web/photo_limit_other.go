//go:build !linux

package web

// limitMemory is Linux only; elsewhere the pixel limit and the decode
// budget have to do.
func limitMemory(pid int, bytes uint64) error { return nil }
