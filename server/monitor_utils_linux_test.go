// +build linux

package server

import (
	"fmt"
	"os"
	"os/exec"
	"testing"
)

func TestComputeProcessCpuAndMem(t *testing.T) {
	var cpu float64
	var mem int64
	cpu, mem = getCPUandMemFromProcfs()

	if cpu < 0 {
		t.Fatalf("Monitored CPU value cannot be less than 0, got: %d\n", cpu)
	}

	if mem < 0 {
		t.Fatalf("Monitored Mem value cannot be less than 0, got: %d\n", mem)
	}

	// Compare with ps output
	var psCpu float64
	var psMem int64
	pidStr := fmt.Sprintf("%d", os.Getpid())
	out, err := exec.Command("ps", "o", "pcpu=,rss=", "-p", pidStr).Output()
	if err != nil {
		t.Fatalf("`ps` failed during test")
	}
	fmt.Sscanf(string(out), "%f %d", &psCpu, &psMem)

	// Value should be similar enough that there shouldn't be too much distance between them
	if cpu+10 < psCpu {
		t.Fatalf("Computed CPU value too dissimilar from ps output, expected: %d, got: %d", psCpu, cpu)
	}

	if mem+1024 < psMem {
		t.Fatalf("Computed Mem value too dissimilar from ps output, expected: %d, got: %d", psMem, mem)
	}
}
