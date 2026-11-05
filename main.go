package main

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

type PageData struct {
	Title       string
	Status      string
	ServerName  string
	CurrentTime string
	Uptime      string

	CPU        string
	CPUPercent float64

	Memory        string
	MemoryPercent float64

	Disk        string
	DiskPercent float64

	BackupDisk        string
	BackupDiskPercent float64
	BackupMounted     bool
}

var (
	cpuUsage float64
	cpuMu    sync.RWMutex
)

type Note struct {
	ID        int64
	Title     string
	Content   string
	CreatedAt time.Time
	UpdatedAt time.Time
}

type Folder struct {
	ID         int64
	Name       string
	ParendId   *int64
	IsPublic   bool
	IsFavorite bool
	IsShared   bool
}

func getMemoryUsage() (string, float64, error) {
	memoryInfo, err := os.ReadFile("/proc/meminfo")
	if err != nil {
		return "", 0, err
	}

	var memTotal, memAvailable float64

	lines := strings.Split(string(memoryInfo), "\n")

	for _, line := range lines {
		if strings.HasPrefix(line, "MemTotal:") {
			fields := strings.Fields(line)

			if len(fields) < 2 {
				return "", 0, fmt.Errorf("invalid MemTotal line")
			}

			memTotal, err = strconv.ParseFloat(fields[1], 64)
			if err != nil {
				return "", 0, err
			}
		}

		if strings.HasPrefix(line, "MemAvailable:") {
			fields := strings.Fields(line)

			if len(fields) < 2 {
				return "", 0, fmt.Errorf("invalid MemAvailable line")
			}

			memAvailable, err = strconv.ParseFloat(fields[1], 64)
			if err != nil {
				return "", 0, err
			}
		}
	}

	if memTotal == 0 {
		return "", 0, fmt.Errorf("MemTotal not found")
	}

	memUsed := memTotal - memAvailable

	memTotalGiB := memTotal / 1024 / 1024
	memUsedGiB := memUsed / 1024 / 1024

	usedPercent := memUsed / memTotal * 100

	result := fmt.Sprintf(
		"%.2f / %.2f GiB (%.2f%%)",
		memUsedGiB,
		memTotalGiB,
		usedPercent,
	)

	return result, usedPercent, nil
}

func getDiskUsage(path string) (string, float64, error) {
	var stat syscall.Statfs_t

	if err := syscall.Statfs(path, &stat); err != nil {
		return "", 0, err
	}

	totalBytes := stat.Blocks * uint64(stat.Bsize)
	freeBytes := stat.Bfree * uint64(stat.Bsize)
	usedBytes := totalBytes - freeBytes

	if totalBytes == 0 {
		return "", 0, fmt.Errorf("disk size is zero")
	}

	const gib = 1024 * 1024 * 1024

	totalGiB := float64(totalBytes) / gib
	usedGiB := float64(usedBytes) / gib

	usedPercent := float64(usedBytes) / float64(totalBytes) * 100

	result := fmt.Sprintf(
		"%.2f / %.2f GiB (%.2f%%)",
		usedGiB,
		totalGiB,
		usedPercent,
	)

	return result, usedPercent, nil
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

func connectDB() (*pgxpool.Pool, error) {
	databaseURL := os.Getenv("DATABASE_URL")
	if databaseURL == "" {
		return nil, fmt.Errorf("DATABASE_URL is not set")
	}

	db, err := pgxpool.New(context.Background(), databaseURL)
	if err != nil {
		return nil, err
	}

	if err := db.Ping(context.Background()); err != nil {
		db.Close()
		return nil, err
	}

	return db, nil
}

func getFolders(ctx context.Context, db *pgxpool.Pool) ([]Folder, error) {
	rows, err := db.Query(
		ctx,
		`SELECT id, name, parent_id, is_public, is_favorite, is_shared
			FROM folders
			ORDER BY id DESC`,
	)

	if err != nil {
		return nil, err
	}

	defer rows.Close()

	var folders []Folder

	for rows.Next() {
		var folder Folder

		if err := rows.Scan(&folder.ID, &folder.Name, &folder.ParendId, &folder.IsPublic, &folder.IsFavorite, &folder.IsShared); err != nil {
			return nil, err
		}

		folders = append(folders, folder)
	}

	if err := rows.Err(); err != nil {
		return nil, err
	}

	return folders, nil
}

func main() {
	db, err := connectDB()
	if err != nil {
		log.Fatalf("database error: %v", err)
	}
	defer db.Close()

	log.Println("PostgreSQL connected")

	go monitorCPU()
	auth := registerPortalAuth(db)
	registerNotesApp(db, auth)

	registerDashboard(auth)

	http.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"status":"ok"}`)
	})

	fs := http.FileServer(http.Dir("static"))
	http.Handle("/static/", http.StripPrefix("/static/", fs))

	if err := http.ListenAndServe("127.0.0.1:8080", auth.middleware(http.DefaultServeMux)); err != nil {
		log.Fatalf("HTTP server error: %v", err)
	}
}
