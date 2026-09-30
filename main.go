package main

import (
	"fmt"
	"html/template"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
)

type PageData struct {
	Title       string
	Status      string
	ServerName  string
	CurrentTime string
	Uptime      string
	Memory      string
	Disk        string
	BackupDisk  string
	CPU         string
}

var (
	cpuUsage float64
	cpuMu    sync.RWMutex
)

func getMemoryUsage() (string, error) {
	memoryinfo, err := os.ReadFile("/proc/meminfo")
	if err != nil {
		return "", err
	}

	var memTotal, memAvailable float64
	lines := strings.Split(string(memoryinfo), "\n")

	for _, line := range lines {
		if strings.HasPrefix(line, "MemTotal:") {
			fields := strings.Fields(line)
			if len(fields) < 2 {
				return "", fmt.Errorf("invalid MemTotal line")
			}

			memTotal, err = strconv.ParseFloat(fields[1], 64)
			if err != nil {
				return "", err
			}
		}

		if strings.HasPrefix(line, "MemAvailable:") {
			fields := strings.Fields(line)
			if len(fields) < 2 {
				return "", fmt.Errorf("invalid MemAvailable line")
			}

			memAvailable, err = strconv.ParseFloat(fields[1], 64)
			if err != nil {
				return "", err
			}
		}
	}

	memUsed := memTotal - memAvailable

	memTotalGiB := memTotal / 1024 / 1024
	memUsedGib := memUsed / 1024 / 1024

	usedPercent := memUsed / memTotal * 100
	return fmt.Sprintf("%.2f / %.2f GiB (%.2f%%)", memUsedGib, memTotalGiB, usedPercent), nil
}

func getDiskUsage(path string) (string, error) {
	var stat syscall.Statfs_t

	if err := syscall.Statfs(path, &stat); err != nil {
		return "", err
	}

	totalBytes := stat.Blocks * uint64(stat.Bsize)
	freeBytes := stat.Bfree * uint64(stat.Bsize)
	usedBytes := totalBytes - freeBytes

	const gib = 1024 * 1024 * 1024

	totalGiB := float64(totalBytes) / gib
	usedGiB := float64(usedBytes) / gib
	usedPercent := float64(usedBytes) / float64(totalBytes) * 100

	return fmt.Sprintf(
		"%.2f / %.2f GiB (%.2f%%)",
		usedGiB,
		totalGiB,
		usedPercent,
	), nil
}

func isMountPoint(path string) (bool, error) {
	var pathStat syscall.Stat_t
	if err := syscall.Stat(path, &pathStat); err != nil {
		return false, err
	}

	parent := filepath.Dir(path)

	var parentStat syscall.Stat_t
	if err := syscall.Stat(parent, &parentStat); err != nil {
		return false, err
	}

	return pathStat.Dev != parentStat.Dev, nil
}

func readCPUStat() (total uint64, idle uint64, err error) {
	data, err := os.ReadFile("/proc/stat")
	if err != nil {
		return 0, 0, err
	}

	lines := strings.Split(string(data), "\n")
	fields := strings.Fields(lines[0])

	if len(fields) < 9 {
		return 0, 0, fmt.Errorf("invalid proc stat format")
	}

	for i := 1; i <= 8; i++ {
		val, err := strconv.ParseUint(fields[i], 10, 64)
		if err != nil {
			return 0, 0, err
		}
		total += val

		// Отдельно считаем idle (индексы 4 и 5)
		if i == 4 || i == 5 {
			idle += val
		}
	}

	return total, idle, nil
}

func getCPUUsage() (float64, error) {
	total1, idle1, err := readCPUStat()
	if err != nil {
		return 0, err
	}

	time.Sleep(500 * time.Millisecond)

	total2, idle2, err := readCPUStat()
	if err != nil {
		return 0, err
	}

	totalDelta := total2 - total1
	idleDelta := idle2 - idle1

	if totalDelta == 0 {
		return 0, nil
	}

	usage := float64(totalDelta-idleDelta) / float64(totalDelta) * 100

	return usage, nil
}

func monitorCPU() {
	for {
		usage, err := getCPUUsage()
		if err == nil {
			cpuMu.Lock()
			cpuUsage = usage
			cpuMu.Unlock()
		}

		time.Sleep(500 * time.Millisecond)
	}
}

func currentCPUUsage() float64 {
	cpuMu.RLock()
	defer cpuMu.RUnlock()

	return cpuUsage
}

func main() {
	go monitorCPU()

	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		tmpl, err := template.ParseFiles("templates/index.html")

		if err != nil {
			http.Error(w, "Ошибка загрузки шаблона", http.StatusInternalServerError)
			return
		}

		w.Header().Set("Content-Type", "text/html; charset=utf-8")

		hostname, err := os.Hostname()
		if err != nil {
			http.Error(w, "Ошибка получения ServerName", http.StatusInternalServerError)
			return
		}

		uptime, err := os.ReadFile("/proc/uptime")
		if err != nil {
			http.Error(w, "Ошибка получения Uptime", http.StatusInternalServerError)
			return
		}

		fields := strings.Fields(string(uptime))

		if len(fields) == 0 {
			http.Error(w, "Некорректные данные Uptime", http.StatusInternalServerError)
			return
		}

		uptimeSeconds, err := strconv.ParseFloat(fields[0], 64)
		if err != nil {
			http.Error(w, "Ошибка конвертации Uptime", http.StatusInternalServerError)
			return
		}

		totalSeconds := int64(uptimeSeconds)

		hours := totalSeconds / 3600
		minutes := (totalSeconds % 3600) / 60

		uptimeStr := fmt.Sprintf("%dh %dm", hours, minutes)

		memory, err := getMemoryUsage()
		if err != nil {
			http.Error(w, "Ошибка получения информации о Memory", http.StatusInternalServerError)
			return
		}

		disk, err := getDiskUsage("/")
		if err != nil {
			http.Error(w, "Ошибка получения информации о диске", http.StatusInternalServerError)
			return
		}

		mounted, err := isMountPoint("/srv/backups")
		if err != nil {
			http.Error(w, "Ошибка проверки backup-диска", http.StatusInternalServerError)
			return
		}

		backupDisk := "Not mounted"

		if mounted {
			backupDisk, err = getDiskUsage("/srv/backups")
			if err != nil {
				http.Error(w, "Ошибка получения информации о backup-диске", http.StatusInternalServerError)
				return
			}
		}

		cpu := fmt.Sprintf("%.1f%%", cpuUsage)

		data := PageData{
			Title:       "Personal Portal",
			Status:      "Server is running",
			ServerName:  hostname,
			CurrentTime: time.Now().Format("02.01.2006 15:04:05"),
			Uptime:      uptimeStr,
			Memory:      memory,
			Disk:        disk,
			BackupDisk:  backupDisk,
			CPU:         cpu,
		}

		tmpl.Execute(w, data)
	})
	http.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"status":"ok"}`)
	})

	http.ListenAndServe(":8080", nil)
}
