// +build !linux

package server

import (
	"fmt"
	"os"
	"os/exec"
	"runtime"
)

// Using `ps` command in non linux builds due to portability issues
func updateUsage(v *Varz) {
	v.Cores = runtime.NumCPU()
	pidStr := fmt.Sprintf("%d", os.Getpid())
	out, err := exec.Command("ps", "o", "pcpu=,rss=", "-p", pidStr).Output()
	if err != nil {
		// FIXME(dlc): Log?
		return
	}
	fmt.Sscanf(string(out), "%f %d", &v.CPU, &v.Mem)
	v.Mem *= 1024 // 1k blocks, want bytes.
}
