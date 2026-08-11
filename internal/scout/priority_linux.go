//go:build linux

package scout

import "syscall"

func setProcessPriority(nice int) error {
	if nice == 0 {
		return nil
	}
	return syscall.Setpriority(syscall.PRIO_PROCESS, 0, nice)
}
