package main

import (
	"context"
	"fmt"
	"html/template"
	"log"
	"net/http"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

type dashboardPage struct {
	Nav           portalNav
	ServerName    string
	CurrentTime   string
	Uptime        string
	CPU           string
	CPUPercent    float64
	Memory        string
	MemoryPercent float64
	Disk          string
	DiskPercent   float64
	BackupDisk    string
	BackupPercent float64
	BackupMounted bool
	IsAdmin       bool
	UserName      string
	CSRFToken     string
}

type dashboardApp struct {
	auth          *portalAuth
	schedulePower func(context.Context, string) error
}

// registerDashboard installs the portal home page and server power controls.
func registerDashboard(auth *portalAuth) {
	app := &dashboardApp{auth: auth, schedulePower: scheduleServerPower}
	http.HandleFunc("GET /{$}", app.page)
	http.HandleFunc("POST /admin/power", app.power)
}

func dashboardUptime() string {
	raw, err := os.ReadFile("/proc/uptime")
	if err != nil {
		return "Нет данных"
	}
	fields := strings.Fields(string(raw))
	if len(fields) == 0 {
		return "Нет данных"
	}
	seconds, err := strconv.ParseFloat(fields[0], 64)
	if err != nil || seconds < 0 {
		return "Нет данных"
	}
	totalMinutes := int64(seconds) / 60
	days := totalMinutes / (24 * 60)
	hours := totalMinutes / 60 % 24
	minutes := totalMinutes % 60
	if days > 0 {
		return fmt.Sprintf("%d д %d ч %d мин", days, hours, minutes)
	}
	return fmt.Sprintf("%d ч %d мин", hours, minutes)
}

func (d *dashboardApp) page(w http.ResponseWriter, r *http.Request) {
	user := d.auth.userFromRequest(r)
	if user == nil {
		http.Error(w, "Требуется вход в аккаунт", http.StatusUnauthorized)
		return
	}

	page := dashboardPage{
		ServerName:  "Сервер",
		CurrentTime: time.Now().Format("02.01.2006 15:04"),
		Uptime:      dashboardUptime(),
		CPUPercent:  currentCPUUsage(),
		IsAdmin:     user.IsAdmin,
		UserName:    user.DisplayName,
		Nav:         newPortalNav(user, d.auth.csrf(r), "dashboard"),
	}
	if page.IsAdmin {
		page.CSRFToken = d.auth.csrf(r)
	}
	if name, err := os.Hostname(); err == nil {
		page.ServerName = name
	}
	page.CPU = fmt.Sprintf("%.1f%%", page.CPUPercent)

	var err error
	page.Memory, page.MemoryPercent, err = getMemoryUsage()
	if err != nil {
		log.Printf("dashboard memory: %v", err)
		page.Memory = "Нет данных"
	}
	page.Disk, page.DiskPercent, err = getDiskUsage("/")
	if err != nil {
		log.Printf("dashboard root disk: %v", err)
		page.Disk = "Нет данных"
	}
	page.BackupMounted, err = isMountPoint("/srv/backups")
	if err != nil {
		log.Printf("dashboard backup mount: %v", err)
	}
	if page.BackupMounted {
		page.BackupDisk, page.BackupPercent, err = getDiskUsage("/srv/backups")
		if err != nil {
			log.Printf("dashboard backup disk: %v", err)
			page.BackupDisk = "Нет данных"
		}
	}

	tmpl, err := template.ParseFiles("templates/index.html", "templates/portal-nav.html")
	if err != nil {
		log.Printf("dashboard template parse: %v", err)
		http.Error(w, "Ошибка загрузки страницы", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := tmpl.Execute(w, page); err != nil {
		log.Printf("dashboard template execute: %v", err)
	}
}

// The only allowed privileged operations are literal "reboot" and "poweroff".
// The root-owned helper schedules execution after the HTTP response is delivered.
func scheduleServerPower(ctx context.Context, action string) error {
	if action != "reboot" && action != "poweroff" {
		return fmt.Errorf("недопустимое действие")
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, "/usr/bin/sudo", "-n", "/usr/local/sbin/personal-portal-power", action)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("запуск команды: %w (%s)", err, strings.TrimSpace(string(out)))
	}
	return nil
}

func (d *dashboardApp) power(w http.ResponseWriter, r *http.Request) {
	user := d.auth.userFromRequest(r)
	if user == nil {
		http.Error(w, "Требуется вход в аккаунт", http.StatusUnauthorized)
		return
	}
	if !user.IsAdmin {
		http.Error(w, "Управление сервером доступно только администратору", http.StatusForbidden)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 2048)
	if err := r.ParseForm(); err != nil {
		http.Error(w, "Некорректная форма", http.StatusBadRequest)
		return
	}
	if !d.auth.verifyCSRF(r, r.PostForm.Get("csrf_token")) {
		http.Error(w, "Истёк токен формы. Обновите страницу.", http.StatusForbidden)
		return
	}
	action := r.PostForm.Get("action")
	if action != "reboot" && action != "poweroff" {
		http.Error(w, "Неизвестное действие", http.StatusBadRequest)
		return
	}
	if r.PostForm.Get("confirm") != "yes" {
		http.Error(w, "Требуется подтверждение", http.StatusBadRequest)
		return
	}

	tmpl, err := template.ParseFiles("templates/power-result.html", "templates/portal-nav.html")
	if err != nil {
		log.Printf("dashboard power template: %v", err)
		http.Error(w, "Ошибка загрузки страницы", http.StatusInternalServerError)
		return
	}
	if err := d.schedulePower(r.Context(), action); err != nil {
		log.Printf("dashboard power scheduling failed: admin=%d action=%s: %v", user.ID, action, err)
		http.Error(w, "Не удалось запланировать действие. Проверьте журнал сервиса и sudoers.", http.StatusInternalServerError)
		return
	}
	log.Printf("dashboard power scheduled: admin=%d action=%s", user.ID, action)
	message := "Сервер перезагрузится через несколько секунд."
	if action == "poweroff" {
		message = "Сервер выключится через несколько секунд. Для включения понадобится Wake-on-LAN или кнопка питания."
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusAccepted)
	if err := tmpl.Execute(w, struct {
		Message string
		Nav     portalNav
	}{Message: message, Nav: newPortalNav(user, d.auth.csrf(r), "dashboard")}); err != nil {
		log.Printf("dashboard power result: %v", err)
	}
}
