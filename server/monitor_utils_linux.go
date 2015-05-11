// +build linux

package server

import (
	"fmt"
	"io/ioutil"
	"runtime"
)

func getCPUandMemFromProcfs() (float64, int64) {

	// Report empty values upon monitoring errors
	statData, err := ioutil.ReadFile("/proc/self/stat")
	if err != nil {
		return 0.0, 0
	}

	uptimeData, err := ioutil.ReadFile("/proc/uptime")
	if err != nil {
		return 0.0, 0
	}

	// Formula to get CPU usage
	// process cpu usage = ((total cpu time * 100) / hertz) / seconds of process life
	// where
	//   total cpu time = time spend in user mode (utime) + time in kernel mode (stime) + time of childs (cutime + cstime)
	//   seconds of process life = seconds since system boot (uptime) - time process started after system boot in seconds (starttime / hertz)
	//   hertz = cycles per seconds in system, as defined in sysconf(_SC_CLK_TCK)
	var utime int64
	var stime int64
	var cutime int64
	var cstime int64
	var starttime int64
	var rss int64
	var secondsSinceBoot float64
	var totalCPUTimeUsed int64
	var secondsOfProcessLife float64

	// FIXME(wallyqs): fmt.Sscanf cannot discard, so capturing everything and ignore
	var ign string

	// FIXME(wallyqs): Should be retrieved from sysconf(_SC_CLK_TCK) value
	hertz := 100.0

	fmt.Sscanf(string(statData), "%s %s %s %s %s %s %s %s %s %s %s %s %s %d %d %d %d %s %s %s %s %d %s %d", &ign, &ign, &ign, &ign, &ign, &ign, &ign, &ign, &ign, &ign, &ign, &ign, &ign, &utime, &stime, &cutime, &cstime, &ign, &ign, &ign, &ign, &starttime, &ign, &rss)
	fmt.Sscanf(string(uptimeData), "%f", &secondsSinceBoot)

	totalCPUTimeUsed = utime + stime + cutime + cstime
	secondsOfProcessLife = secondsSinceBoot - (float64(starttime) / hertz)
	pcpu := (float64(totalCPUTimeUsed) * 100 / hertz) / secondsOfProcessLife

	return pcpu, rss
}

func updateUsage(v *Varz) {
	v.Cores = runtime.NumCPU()
	v.CPU, v.Mem = getCPUandMemFromProcfs()
	v.Mem *= 1024 // 1k blocks, want bytes.
}
