package main

import (
	"context"
	"fmt"
	"html/template"
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
	ID      int64
	Title   string
	Content string
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

func main() {
	db, err := connectDB()
	if err != nil {
		log.Fatalf("database error: %v", err)
	}
	defer db.Close()

	log.Println("PostgreSQL connected")

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

		cpuUsage := currentCPUUsage()
		cpu := fmt.Sprintf("%.1f%%", cpuUsage)

		memory, memoryPercent, err := getMemoryUsage()
		if err != nil {
			http.Error(w, "Ошибка получения информации о Memory", http.StatusInternalServerError)
			return
		}

		disk, diskPercent, err := getDiskUsage("/")
		if err != nil {
			http.Error(w, "Ошибка получения информации о диске", http.StatusInternalServerError)
			return
		}

		backupMounted, err := isMountPoint("/srv/backups")
		if err != nil {
			http.Error(w, "Ошибка проверки backup-диска", http.StatusInternalServerError)
			return
		}

		backupDisk := "Not mounted"
		backupDiskPercent := float64(0)

		if backupMounted {
			backupDisk, backupDiskPercent, err = getDiskUsage("/srv/backups")
			if err != nil {
				http.Error(w, "Ошибка получения информации о backup-диске", http.StatusInternalServerError)
				return
			}
		}

		data := PageData{
			Title:       "Personal Portal",
			Status:      "Server is running",
			ServerName:  hostname,
			CurrentTime: time.Now().Format("02.01.2006 15:04:05"),
			Uptime:      uptimeStr,

			CPU:        cpu,
			CPUPercent: cpuUsage,

			Memory:        memory,
			MemoryPercent: memoryPercent,

			Disk:        disk,
			DiskPercent: diskPercent,

			BackupDisk:        backupDisk,
			BackupDiskPercent: backupDiskPercent,
			BackupMounted:     backupMounted,
		}

		tmpl.Execute(w, data)
	})

	http.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"status":"ok"}`)
	})

	http.HandleFunc("/notes", func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			title := r.FormValue("title")
			content := r.FormValue("content")

			_, err := db.Exec(
				r.Context(),
				`INSERT INTO notes (title, content)
				VALUES ($1, $2)`,
				title,
				content,
			)
			if err != nil {
				http.Error(w, "Ошибка создания заметки", http.StatusInternalServerError)
				return
			}

			http.Redirect(w, r, "/notes", http.StatusSeeOther)
			return
		}

		rows, err := db.Query(
			r.Context(),
			`SELECT id, title, content
			FROM notes
			ORDER BY id DESC`,
		)
		if err != nil {
			http.Error(w, "Ошибка получения заметок", http.StatusInternalServerError)
			return
		}
		defer rows.Close()

		var notes []Note

		for rows.Next() {
			var note Note

			if err := rows.Scan(&note.ID, &note.Title, &note.Content); err != nil {
				http.Error(w, "Ошибка чтения заметки", http.StatusInternalServerError)
				return
			}

			notes = append(notes, note)
		}

		if err := rows.Err(); err != nil {
			http.Error(w, "Ошибка чтения заметок", http.StatusInternalServerError)
			return
		}

		tmpl, err := template.ParseFiles("templates/notes.html")
		if err != nil {
			http.Error(w, "Ошибка загрузки шаблона", http.StatusInternalServerError)
			return
		}

		w.Header().Set("Content-Type", "text/html; charset=utf-8")

		if err := tmpl.Execute(w, notes); err != nil {
			log.Printf("template error: %v", err)
		}
	})

	http.HandleFunc("/notes/delete", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
			return
		}

		idStr := r.FormValue("id")

		id, err := strconv.ParseInt(idStr, 10, 64)
		if err != nil {
			http.Error(w, "Некорректный ID заметки", http.StatusBadRequest)
			return
		}

		_, err = db.Exec(
			r.Context(),
			`DELETE FROM notes WHERE id = $1`,
			id,
		)
		if err != nil {
			http.Error(w, "Ошибка удаления заметки", http.StatusInternalServerError)
			return
		}

		http.Redirect(w, r, "/notes", http.StatusSeeOther)
	})

	http.HandleFunc("/notes/update", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
			return
		}

		idStr := r.FormValue("id")
		title := r.FormValue("title")
		content := r.FormValue("content")

		id, err := strconv.ParseInt(idStr, 10, 64)
		if err != nil {
			http.Error(w, "Некорректный ID заметки", http.StatusBadRequest)
			return
		}

		if title == "" {
			http.Error(w, "Заголовок не может быть пустым", http.StatusBadRequest)
			return
		}

		_, err = db.Exec(
			r.Context(),
			`UPDATE notes
			SET title = $1,
				content = $2,
				updated_at = NOW()
			WHERE id = $3`,
			title,
			content,
			id,
		)
		if err != nil {
			http.Error(w, "Ошибка обновления заметки", http.StatusInternalServerError)
			return
		}

		http.Redirect(w, r, "/notes", http.StatusSeeOther)
	})

	fs := http.FileServer(http.Dir("static"))
	http.Handle("/static/", http.StripPrefix("/static/", fs))

	http.ListenAndServe(":8080", nil)
}
