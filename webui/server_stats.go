package webui

import (
	"bytes"
	"fmt"
	"math"
	"os"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
	"time"

	"diana-qq-bot/model/storage"
)

var webuiProcessStartedAt = time.Now()

func collectDashboardServerStats(now time.Time) storage.DashboardServerStats {
	if now.IsZero() {
		now = time.Now()
	}
	var mem runtime.MemStats
	runtime.ReadMemStats(&mem)
	hostname, _ := os.Hostname()
	stats := storage.DashboardServerStats{
		CollectedAt:          now,
		Hostname:             strings.TrimSpace(hostname),
		OS:                   runtime.GOOS,
		Arch:                 runtime.GOARCH,
		ProcessID:            os.Getpid(),
		ProcessUptimeSeconds: int64(now.Sub(webuiProcessStartedAt).Seconds()),
		CPUModel:             cpuModel(),
		CPUCores:             runtime.NumCPU(),
		GoHeapAllocBytes:     mem.HeapAlloc,
		GoHeapSystemBytes:    mem.HeapSys,
		GoRoutines:           runtime.NumGoroutine(),
		RuntimeVersion:       runtime.Version(),
	}
	if usage, err := totalCPUUsagePercent(stats.CPUCores); err == nil {
		stats.CPUUsagePercent = usage
	} else {
		stats.MetricsUnavailableReason = err.Error()
	}
	if total, used, err := memoryUsage(); err == nil {
		stats.MemoryTotalBytes = total
		stats.MemoryUsedBytes = used
		if total > 0 {
			stats.MemoryUsagePercent = roundPercent((float64(used) / float64(total)) * 100)
		}
	} else if stats.MetricsUnavailableReason == "" {
		stats.MetricsUnavailableReason = err.Error()
	}
	if procCPU, procMem, err := processUsage(os.Getpid()); err == nil {
		stats.ProcessCPUPercent = procCPU
		stats.ProcessMemoryBytes = procMem
	} else {
		stats.ProcessMetricsUnavailable = err.Error()
	}
	return stats
}

func cpuModel() string {
	switch runtime.GOOS {
	case "darwin":
		return commandOutput("sysctl", "-n", "machdep.cpu.brand_string")
	case "linux":
		data, err := os.ReadFile("/proc/cpuinfo")
		if err != nil {
			return ""
		}
		for _, line := range strings.Split(string(data), "\n") {
			key, value, ok := strings.Cut(line, ":")
			if ok && strings.EqualFold(strings.TrimSpace(key), "model name") {
				return strings.TrimSpace(value)
			}
		}
	}
	return ""
}

func totalCPUUsagePercent(cores int) (float64, error) {
	if cores <= 0 {
		cores = 1
	}
	output := commandOutput("ps", "-A", "-o", "%cpu=")
	if strings.TrimSpace(output) == "" {
		return 0, fmt.Errorf("cpu usage command unavailable")
	}
	var total float64
	for _, field := range strings.Fields(output) {
		value, err := strconv.ParseFloat(strings.TrimSpace(field), 64)
		if err == nil {
			total += value
		}
	}
	return clampPercent(roundPercent(total / float64(cores))), nil
}

func memoryUsage() (uint64, uint64, error) {
	switch runtime.GOOS {
	case "darwin":
		total, err := darwinTotalMemory()
		if err != nil {
			return 0, 0, err
		}
		used, err := darwinUsedMemory()
		if err != nil {
			return total, 0, err
		}
		return total, used, nil
	case "linux":
		return linuxMemoryUsage()
	default:
		return 0, 0, fmt.Errorf("memory metrics unsupported on %s", runtime.GOOS)
	}
}

func darwinTotalMemory() (uint64, error) {
	output := commandOutput("sysctl", "-n", "hw.memsize")
	total, err := strconv.ParseUint(strings.TrimSpace(output), 10, 64)
	if err != nil {
		return 0, fmt.Errorf("read total memory: %w", err)
	}
	return total, nil
}

func darwinUsedMemory() (uint64, error) {
	output := commandOutput("vm_stat")
	if strings.TrimSpace(output) == "" {
		return 0, fmt.Errorf("vm_stat unavailable")
	}
	pageSize := uint64(4096)
	var active, wired, compressed uint64
	for _, line := range strings.Split(output, "\n") {
		if strings.Contains(line, "page size of") {
			for _, field := range strings.Fields(line) {
				if value, err := strconv.ParseUint(field, 10, 64); err == nil && value > 0 {
					pageSize = value
					break
				}
			}
			continue
		}
		key, rawValue, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		value, err := strconv.ParseUint(strings.Trim(strings.TrimSpace(rawValue), "."), 10, 64)
		if err != nil {
			continue
		}
		switch strings.TrimSpace(key) {
		case "Pages active":
			active = value
		case "Pages wired down":
			wired = value
		case "Pages occupied by compressor":
			compressed = value
		}
	}
	return (active + wired + compressed) * pageSize, nil
}

func linuxMemoryUsage() (uint64, uint64, error) {
	data, err := os.ReadFile("/proc/meminfo")
	if err != nil {
		return 0, 0, err
	}
	values := map[string]uint64{}
	for _, line := range strings.Split(string(data), "\n") {
		key, rawValue, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		fields := strings.Fields(rawValue)
		if len(fields) == 0 {
			continue
		}
		value, err := strconv.ParseUint(fields[0], 10, 64)
		if err == nil {
			values[key] = value * 1024
		}
	}
	total := values["MemTotal"]
	available := values["MemAvailable"]
	if total == 0 {
		return 0, 0, fmt.Errorf("MemTotal unavailable")
	}
	if available > total {
		available = 0
	}
	return total, total - available, nil
}

func processUsage(pid int) (float64, uint64, error) {
	output := commandOutput("ps", "-p", strconv.Itoa(pid), "-o", "%cpu=,rss=")
	fields := strings.Fields(output)
	if len(fields) < 2 {
		return 0, 0, fmt.Errorf("process usage unavailable")
	}
	cpu, err := strconv.ParseFloat(fields[0], 64)
	if err != nil {
		return 0, 0, err
	}
	rssKB, err := strconv.ParseUint(fields[1], 10, 64)
	if err != nil {
		return 0, 0, err
	}
	return roundPercent(cpu), rssKB * 1024, nil
}

func commandOutput(name string, args ...string) string {
	cmd := exec.Command(name, args...)
	var stdout bytes.Buffer
	cmd.Stdout = &stdout
	if err := cmd.Run(); err != nil {
		return ""
	}
	return strings.TrimSpace(stdout.String())
}

func roundPercent(value float64) float64 {
	if math.IsNaN(value) || math.IsInf(value, 0) {
		return 0
	}
	return math.Round(value*100) / 100
}

func clampPercent(value float64) float64 {
	if value < 0 {
		return 0
	}
	if value > 100 {
		return 100
	}
	return value
}
