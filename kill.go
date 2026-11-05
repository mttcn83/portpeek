//go:build linux

package main

import (
	"syscall"
	"time"
)

// pidfd syscalls: same numbers on every architecture with the generic table
// (amd64, arm64, riscv64, ...). Needs Linux 5.3+.
const (
	sysPidfdOpen       = 434
	sysPidfdSendSignal = 424
)

const killTimeout = 3 * time.Second // how long to wait for exit after a signal

// terminate signals pid if it still owns the port.
//
// status: stopped | gone | changed | denied | alive | unsupported. A pidfd
// pins the process, so a recycled PID can't be hit; ownership is re-checked
// after pinning.
func terminate(pid int, inodes map[uint64]bool, force bool, timeout time.Duration) (string, time.Duration) {
	fd, _, e := syscall.Syscall(sysPidfdOpen, uintptr(pid), 0, 0)
	switch e {
	case 0:
	case syscall.ESRCH:
		return "gone", 0
	default:
		return "unsupported", 0
	}
	pfd := int(fd)
	defer syscall.Close(pfd)

	owns := false
	for _, in := range socketInodes(pid) {
		owns = owns || inodes[in]
	}
	if !owns {
		return "changed", 0
	}
	sig := syscall.SIGTERM
	if force {
		sig = syscall.SIGKILL
	}
	start := time.Now()
	if _, _, e := syscall.Syscall6(sysPidfdSendSignal, fd, uintptr(sig), 0, 0, 0, 0); e != 0 {
		switch e {
		case syscall.EPERM:
			return "denied", 0
		case syscall.ESRCH:
			return "gone", 0
		}
		return "unsupported", 0
	}
	if ok, _ := waitReadable(pfd, timeout); ok { // readable once the process exits
		return "stopped", time.Since(start)
	}
	return "alive", timeout
}

func waitReadable(fd int, timeout time.Duration) (bool, error) {
	ep, err := syscall.EpollCreate1(syscall.EPOLL_CLOEXEC)
	if err != nil {
		return false, err
	}
	defer syscall.Close(ep)
	ev := syscall.EpollEvent{Events: syscall.EPOLLIN, Fd: int32(fd)}
	if err := syscall.EpollCtl(ep, syscall.EPOLL_CTL_ADD, fd, &ev); err != nil {
		return false, err
	}
	deadline := time.Now().Add(timeout)
	var out [1]syscall.EpollEvent
	for {
		left := max(0, time.Until(deadline))
		n, err := syscall.EpollWait(ep, out[:], int((left+time.Millisecond-1)/time.Millisecond))
		if err == syscall.EINTR {
			continue
		}
		return n > 0, err
	}
}
