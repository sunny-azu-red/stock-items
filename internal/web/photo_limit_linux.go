package web

import (
	"syscall"
	"unsafe"
)

// limitMemory sets the data limit (RLIMIT_DATA: heap and anonymous
// mappings) of process pid, so a decoder that wants more gets an
// allocation failure instead of the machine's memory. Go can't set a
// limit for a child before it starts, so runVips does this before the
// child has its input.
func limitMemory(pid int, bytes uint64) error {
	lim := syscall.Rlimit{Cur: bytes, Max: bytes}
	_, _, errno := syscall.RawSyscall6(syscall.SYS_PRLIMIT64, uintptr(pid), syscall.RLIMIT_DATA,
		uintptr(unsafe.Pointer(&lim)), 0, 0, 0)
	if errno != 0 {
		return errno
	}
	return nil
}
