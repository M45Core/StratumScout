//go:build !linux

package scout

import "fmt"

func setProcessPriority(nice int) error {
	if nice == 0 {
		return nil
	}
	return fmt.Errorf("PROCESS_NICE is unsupported on this platform")
}
