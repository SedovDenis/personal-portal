package main

import (
	"fmt"
	"html/template"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"
)

type PageData struct {
	Title       string
	Status      string
	ServerName  string
	CurrentTime string
	Uptime      string
	Memory      string
}

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

func main() {
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

		data := PageData{
			Title:       "Personal Portal",
			Status:      "Server is running",
			ServerName:  hostname,
			CurrentTime: time.Now().Format("02.01.2006 15:04:05"),
			Uptime:      uptimeStr,
			Memory:      memory,
		}

		tmpl.Execute(w, data)
	})
	http.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"status":"ok"}`)
	})

	http.ListenAndServe(":8080", nil)
}
