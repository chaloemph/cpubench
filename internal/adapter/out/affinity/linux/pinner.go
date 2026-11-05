// Package linux pins OS threads with sched_setaffinity.
package linux

import (
	"golang.org/x/sys/unix"
)

type Pinner struct{}

// PinCurrentThread binds the calling thread (tid 0 = self) to one CPU.
// The caller must hold runtime.LockOSThread.
func (Pinner) PinCurrentThread(cpu int) error {
	var set unix.CPUSet
	set.Set(cpu)
	return unix.SchedSetaffinity(0, &set)
}
