package main

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"fmt"
	"html/template"
	"log"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/SedovDenis/personal-portal/internal/authhash"
	"github.com/jackc/pgx/v5/pgxpool"
)

type portalUser struct {
	ID          int64
	Username    string
	DisplayName string
	IsAdmin     bool
}
type portalUserKey struct{}
type loginAttempts struct {
	Count int
	Since time.Time
}
type portalAuth struct {
	db         *pgxpool.Pool
	csrfSecret [32]byte
	attemptsMu sync.Mutex
	attempts   map[string]loginAttempts
	loginTmpl  *template.Template
	usersTmpl  *template.Template
}

func registerPortalAuth(db *pgxpool.Pool) *portalAuth {
	a := &portalAuth{db: db, attempts: make(map[string]loginAttempts)}
	if _, err := rand.Read(a.csrfSecret[:]); err != nil {
		log.Fatalf("auth random: %v", err)
	}
	var err error
	a.loginTmpl, err = template.ParseFiles("templates/login.html")
	if err != nil {
		log.Fatalf("auth template: %v", err)
	}
	a.usersTmpl, err = template.ParseFiles("templates/users.html")
	if err != nil {
		log.Fatalf("users template: %v", err)
	}
	http.HandleFunc("GET /auth", a.loginPage)
	http.HandleFunc("POST /auth/login", a.login)
	http.HandleFunc("POST /auth/logout", a.logout)
	http.HandleFunc("GET /auth/users", a.usersPage)
	http.HandleFunc("POST /auth/users", a.createUser)
	http.HandleFunc("POST /auth/users/update", a.updateUser)
	http.HandleFunc("POST /auth/users/delete", a.deleteUser)
	return a
}
func newSecret() string {
	var b [32]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic(err)
	}
	return base64.RawURLEncoding.EncodeToString(b[:])
}
func (a *portalAuth) userFromRequest(r *http.Request) *portalUser {
	u, _ := r.Context().Value(portalUserKey{}).(*portalUser)
	return u
}
func (a *portalAuth) lookupSession(ctx context.Context, token string) (*portalUser, error) {
	if len(token) != 43 {
		return nil, nil
	}
	raw, err := base64.RawURLEncoding.DecodeString(token)
	if err != nil || len(raw) != 32 {
		return nil, nil
	}
	digest := sha256.Sum256(raw)
	var u portalUser
	err = a.db.QueryRow(ctx, `SELECT u.id,u.username,u.display_name,u.is_admin FROM portal_sessions s JOIN portal_users u ON u.id=s.user_id WHERE s.token_hash=$1 AND s.expires_at>NOW()`, digest[:]).Scan(&u.ID, &u.Username, &u.DisplayName, &u.IsAdmin)
	if err != nil {
		return nil, nil
	}
	return &u, nil
}
func (a *portalAuth) middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "same-origin")
		if !strings.HasPrefix(r.URL.Path, "/static/") && r.URL.Path != "/health" {
			w.Header().Set("Cache-Control", "no-store")
		}
		// Only static assets, health checks, and login are anonymous.
		if strings.HasPrefix(r.URL.Path, "/static/") || r.URL.Path == "/health" || r.URL.Path == "/auth" || r.URL.Path == "/auth/login" {
			next.ServeHTTP(w, r)
			return
		}
		var u *portalUser
		if cookie, err := r.Cookie("pp_session"); err == nil {
			u, _ = a.lookupSession(r.Context(), cookie.Value)
		}
		if u == nil {
			if r.Method == http.MethodGet {
				http.Redirect(w, r, "/auth?next="+url.QueryEscape(r.URL.RequestURI()), http.StatusSeeOther)
			} else {
				http.Error(w, "Сессия завершена. Войдите снова.", http.StatusUnauthorized)
			}
			return
		}
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), portalUserKey{}, u)))
	})
}
func secureCookie(name, val string, maxAge int) *http.Cookie {
	return &http.Cookie{Name: name, Value: val, Path: "/", HttpOnly: true, Secure: true, SameSite: http.SameSiteLaxMode, MaxAge: maxAge}
}
func validNext(next string) string {
	if !strings.HasPrefix(next, "/") || strings.HasPrefix(next, "//") || strings.ContainsAny(next, "\r\n\\") {
		return "/notes"
	}
	return next
}
func (a *portalAuth) loginPage(w http.ResponseWriter, r *http.Request) {
	cookie, err := r.Cookie("pp_login_csrf")
	if err != nil || len(cookie.Value) != 43 {
		cookie = secureCookie("pp_login_csrf", newSecret(), 0)
		http.SetCookie(w, cookie)
	}
	var remembered string
	if last, err := r.Cookie("pp_last_user"); err == nil {
		remembered = last.Value
	}
	page := struct{ CSRF, Username, Next, Error string }{cookie.Value, remembered, validNext(r.URL.Query().Get("next")), ""}
	if r.URL.Query().Get("error") == "credentials" {
		page.Error = "Неправильный логин или пароль"
	}
	if r.URL.Query().Get("error") == "limited" {
		page.Error = "Слишком много попыток. Повторите через 15 минут"
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := a.loginTmpl.Execute(w, page); err != nil {
		log.Printf("login template: %v", err)
	}
}
func (a *portalAuth) checkLoginCSRF(r *http.Request) bool {
	c, err := r.Cookie("pp_login_csrf")
	return err == nil && c.Value != "" && subtle.ConstantTimeCompare([]byte(c.Value), []byte(r.PostForm.Get("csrf_token"))) == 1
}
func (a *portalAuth) isLimited(key string) bool {
	a.attemptsMu.Lock()
	defer a.attemptsMu.Unlock()
	attempt, ok := a.attempts[key]
	if !ok || time.Since(attempt.Since) > 15*time.Minute {
		delete(a.attempts, key)
		return false
	}
	return attempt.Count >= 5
}
func (a *portalAuth) recordFailure(key string) {
	a.attemptsMu.Lock()
	defer a.attemptsMu.Unlock()
	attempt := a.attempts[key]
	if time.Since(attempt.Since) > 15*time.Minute {
		attempt = loginAttempts{Since: time.Now()}
	}
	attempt.Count++
	a.attempts[key] = attempt
	if len(a.attempts) > 2000 {
		a.attempts = make(map[string]loginAttempts)
	}
}
func (a *portalAuth) login(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 4096)
	if err := r.ParseForm(); err != nil || !a.checkLoginCSRF(r) {
		http.Error(w, "Некорректная форма входа", http.StatusForbidden)
		return
	}
	username := strings.ToLower(strings.TrimSpace(r.PostForm.Get("username")))
	password := r.PostForm.Get("password")
	next := validNext(r.PostForm.Get("next"))
	if a.isLimited(username) {
		http.Redirect(w, r, "/auth?error=limited&next="+url.QueryEscape(next), http.StatusSeeOther)
		return
	}
	var u portalUser
	var hash string
	err := a.db.QueryRow(r.Context(), `SELECT id,username,display_name,is_admin,password_hash FROM portal_users WHERE username=$1`, username).Scan(&u.ID, &u.Username, &u.DisplayName, &u.IsAdmin, &hash)
	if err != nil || !authhash.Verify(password, hash) {
		a.recordFailure(username)
		http.Redirect(w, r, "/auth?error=credentials&next="+url.QueryEscape(next), http.StatusSeeOther)
		return
	}
	raw := newSecret()
	decoded, _ := base64.RawURLEncoding.DecodeString(raw)
	digest := sha256.Sum256(decoded)
	if _, err := a.db.Exec(r.Context(), `INSERT INTO portal_sessions(token_hash,user_id,expires_at) VALUES ($1,$2,NOW()+INTERVAL '7 days')`, digest[:], u.ID); err != nil {
		http.Error(w, "Ошибка создания сессии", 500)
		log.Printf("auth session: %v", err)
		return
	}
	if old, err := r.Cookie("pp_session"); err == nil {
		a.revoke(r.Context(), old.Value)
	}
	http.SetCookie(w, secureCookie("pp_session", raw, 0)) // browser-session cookie
	http.SetCookie(w, secureCookie("pp_last_user", u.Username, 60*60*24*365))
	a.attemptsMu.Lock()
	delete(a.attempts, username)
	a.attemptsMu.Unlock()
	http.Redirect(w, r, next, http.StatusSeeOther)
}
func (a *portalAuth) revoke(ctx context.Context, token string) {
	raw, err := base64.RawURLEncoding.DecodeString(token)
	if err != nil || len(raw) != 32 {
		return
	}
	digest := sha256.Sum256(raw)
	if _, err := a.db.Exec(ctx, `DELETE FROM portal_sessions WHERE token_hash=$1`, digest[:]); err != nil {
		log.Printf("auth revoke: %v", err)
	}
}
func (a *portalAuth) csrf(r *http.Request) string {
	c, err := r.Cookie("pp_session")
	if err != nil {
		return ""
	}
	mac := hmac.New(sha256.New, a.csrfSecret[:])
	mac.Write([]byte(c.Value))
	return base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}
func (a *portalAuth) verifyCSRF(r *http.Request, provided string) bool {
	expected := a.csrf(r)
	return expected != "" && subtle.ConstantTimeCompare([]byte(expected), []byte(provided)) == 1
}
func (a *portalAuth) logout(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 1024)
	if err := r.ParseForm(); err != nil || !a.verifyCSRF(r, r.PostForm.Get("csrf_token")) {
		http.Error(w, "Неверный токен формы", 403)
		return
	}
	if c, err := r.Cookie("pp_session"); err == nil {
		a.revoke(r.Context(), c.Value)
	}
	http.SetCookie(w, secureCookie("pp_session", "", -1))
	http.Redirect(w, r, "/auth", http.StatusSeeOther)
}
func (a *portalAuth) listUsers(ctx context.Context, exclude int64) ([]portalUser, error) {
	rows, err := a.db.Query(ctx, `SELECT id,username,display_name,is_admin FROM portal_users WHERE id<>$1 ORDER BY lower(display_name),id`, exclude)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	users := []portalUser{}
	for rows.Next() {
		var u portalUser
		if err := rows.Scan(&u.ID, &u.Username, &u.DisplayName, &u.IsAdmin); err != nil {
			return nil, err
		}
		users = append(users, u)
	}
	return users, rows.Err()
}
func (a *portalAuth) usersPage(w http.ResponseWriter, r *http.Request) {
	u := a.userFromRequest(r)
	if u == nil || !u.IsAdmin {
		http.Error(w, "Доступ только администратору", 403)
		return
	}
	users, err := a.listUsers(r.Context(), 0)
	if err != nil {
		http.Error(w, "Ошибка пользователей", 500)
		return
	}
	p := struct {
		Current        *portalUser
		Users          []portalUser
		Token, Message string
	}{u, users, a.csrf(r), ""}
	if r.URL.Query().Get("created") == "1" {
		p.Message = "Пользователь создан"
	}
	switch r.URL.Query().Get("error") {
	case "1":
		p.Message = "Неверные данные или такой логин уже существует"
	case "notfound":
		p.Message = "Пользователь не найден"
	case "selfadmin":
		p.Message = "Нельзя снять права администратора с собственного аккаунта"
	case "selfdelete":
		p.Message = "Нельзя удалить собственный аккаунт"
	case "lastadmin":
		p.Message = "В системе должен остаться хотя бы один администратор"
	case "database":
		p.Message = "Не удалось сохранить изменения. Проверьте журнал приложения"
	}
	if r.URL.Query().Get("updated") == "1" {
		p.Message = "Данные пользователя обновлены"
	}
	if r.URL.Query().Get("deleted") == "1" {
		p.Message = "Пользователь удалён. Его папки и шаблоны переданы вам; его черновики удалены"
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := a.usersTmpl.Execute(w, p); err != nil {
		log.Printf("users template: %v", err)
	}
}

var userNamePattern = regexp.MustCompile(`^[a-zA-Z0-9_.-]{3,40}$`)

func (a *portalAuth) createUser(w http.ResponseWriter, r *http.Request) {
	u := a.userFromRequest(r)
	if u == nil || !u.IsAdmin {
		http.Error(w, "Недостаточно прав", 403)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 4096)
	if err := r.ParseForm(); err != nil || !a.verifyCSRF(r, r.PostForm.Get("csrf_token")) {
		http.Error(w, "Неверная форма", 403)
		return
	}
	name := strings.ToLower(strings.TrimSpace(r.PostForm.Get("username")))
	display := strings.TrimSpace(r.PostForm.Get("display_name"))
	password := r.PostForm.Get("password")
	if !validPortalUserDetails(name, display) {
		http.Redirect(w, r, "/auth/users?error=1", 303)
		return
	}
	hash, err := authhash.Hash(password)
	if err != nil {
		http.Redirect(w, r, "/auth/users?error=1", 303)
		return
	}
	if _, err := a.db.Exec(r.Context(), `INSERT INTO portal_users(username,display_name,password_hash) VALUES ($1,$2,$3)`, name, display, hash); err != nil {
		log.Printf("auth create user: %v", err)
		http.Redirect(w, r, "/auth/users?error=1", 303)
		return
	}
	http.Redirect(w, r, "/auth/users?created=1", 303)
}

// Full account management is available only to administrators. Changes to admin
// privileges and account deletion serialize through a table lock, preventing
// concurrent requests from accidentally removing the last administrator.
func validPortalUserDetails(username, display string) bool {
	return userNamePattern.MatchString(username) && display != "" && utf8.RuneCountInString(display) <= 120
}

func portalUserID(r *http.Request) (int64, error) {
	id, err := strconv.ParseInt(strings.TrimSpace(r.PostForm.Get("id")), 10, 64)
	if err != nil || id < 1 {
		return 0, fmt.Errorf("invalid user ID")
	}
	return id, nil
}

func (a *portalAuth) adminForm(w http.ResponseWriter, r *http.Request) (*portalUser, bool) {
	u := a.userFromRequest(r)
	if u == nil || !u.IsAdmin {
		http.Error(w, "Доступ только администратору", http.StatusForbidden)
		return nil, false
	}
	r.Body = http.MaxBytesReader(w, r.Body, 4096)
	if err := r.ParseForm(); err != nil || !a.verifyCSRF(r, r.PostForm.Get("csrf_token")) {
		http.Error(w, "Неверная форма или токен безопасности", http.StatusForbidden)
		return nil, false
	}
	return u, true
}

func (a *portalAuth) updateUser(w http.ResponseWriter, r *http.Request) {
	admin, ok := a.adminForm(w, r)
	if !ok {
		return
	}
	id, err := portalUserID(r)
	if err != nil {
		http.Error(w, "Некорректный ID пользователя", http.StatusBadRequest)
		return
	}
	username := strings.ToLower(strings.TrimSpace(r.PostForm.Get("username")))
	display := strings.TrimSpace(r.PostForm.Get("display_name"))
	password := r.PostForm.Get("password")
	isAdmin := r.PostForm.Get("is_admin") == "1"
	if !validPortalUserDetails(username, display) {
		http.Redirect(w, r, "/auth/users?error=1", http.StatusSeeOther)
		return
	}
	if id == admin.ID && !isAdmin {
		http.Redirect(w, r, "/auth/users?error=selfadmin", http.StatusSeeOther)
		return
	}

	var passwordHash string
	if password != "" { // An empty password field means: keep the existing one.
		passwordHash, err = authhash.Hash(password)
		if err != nil {
			http.Redirect(w, r, "/auth/users?error=1", http.StatusSeeOther)
			return
		}
	}
	ctx := r.Context()
	tx, err := a.db.Begin(ctx)
	if err != nil {
		log.Printf("auth update user begin: %v", err)
		http.Redirect(w, r, "/auth/users?error=database", http.StatusSeeOther)
		return
	}
	defer tx.Rollback(ctx)
	if _, err = tx.Exec(ctx, `LOCK TABLE portal_users IN SHARE ROW EXCLUSIVE MODE`); err != nil {
		log.Printf("auth update user lock: %v", err)
		http.Redirect(w, r, "/auth/users?error=database", http.StatusSeeOther)
		return
	}
	var wasAdmin bool
	if err = tx.QueryRow(ctx, `SELECT is_admin FROM portal_users WHERE id=$1`, id).Scan(&wasAdmin); err != nil {
		http.Redirect(w, r, "/auth/users?error=notfound", http.StatusSeeOther)
		return
	}
	if wasAdmin && !isAdmin {
		var adminCount int
		if err = tx.QueryRow(ctx, `SELECT COUNT(*) FROM portal_users WHERE is_admin`).Scan(&adminCount); err != nil {
			log.Printf("auth count admins: %v", err)
			http.Redirect(w, r, "/auth/users?error=database", http.StatusSeeOther)
			return
		}
		if adminCount <= 1 {
			http.Redirect(w, r, "/auth/users?error=lastadmin", http.StatusSeeOther)
			return
		}
	}
	if passwordHash != "" {
		_, err = tx.Exec(ctx, `UPDATE portal_users SET username=$1,display_name=$2,is_admin=$3,password_hash=$4 WHERE id=$5`, username, display, isAdmin, passwordHash, id)
	} else {
		_, err = tx.Exec(ctx, `UPDATE portal_users SET username=$1,display_name=$2,is_admin=$3 WHERE id=$4`, username, display, isAdmin, id)
	}
	if err != nil {
		log.Printf("auth update user: %v", err)
		http.Redirect(w, r, "/auth/users?error=1", http.StatusSeeOther)
		return
	}
	if passwordHash != "" {
		// Password reset revokes all previously issued sessions, on all devices.
		if _, err = tx.Exec(ctx, `DELETE FROM portal_sessions WHERE user_id=$1`, id); err != nil {
			log.Printf("auth revoke reset sessions: %v", err)
			http.Redirect(w, r, "/auth/users?error=database", http.StatusSeeOther)
			return
		}
	}
	if err = tx.Commit(ctx); err != nil {
		log.Printf("auth update user commit: %v", err)
		http.Redirect(w, r, "/auth/users?error=database", http.StatusSeeOther)
		return
	}
	if id == admin.ID {
		http.SetCookie(w, secureCookie("pp_last_user", username, 60*60*24*365))
		if passwordHash != "" {
			http.SetCookie(w, secureCookie("pp_session", "", -1))
			http.Redirect(w, r, "/auth?next=/auth/users", http.StatusSeeOther)
			return
		}
	}
	http.Redirect(w, r, "/auth/users?updated=1", http.StatusSeeOther)
}

func (a *portalAuth) deleteUser(w http.ResponseWriter, r *http.Request) {
	admin, ok := a.adminForm(w, r)
	if !ok {
		return
	}
	id, err := portalUserID(r)
	if err != nil {
		http.Error(w, "Некорректный ID пользователя", http.StatusBadRequest)
		return
	}
	if id == admin.ID {
		http.Redirect(w, r, "/auth/users?error=selfdelete", http.StatusSeeOther)
		return
	}
	ctx := r.Context()
	tx, err := a.db.Begin(ctx)
	if err != nil {
		log.Printf("auth delete user begin: %v", err)
		http.Redirect(w, r, "/auth/users?error=database", http.StatusSeeOther)
		return
	}
	defer tx.Rollback(ctx)
	if _, err = tx.Exec(ctx, `LOCK TABLE portal_users IN SHARE ROW EXCLUSIVE MODE`); err != nil {
		log.Printf("auth delete user lock: %v", err)
		http.Redirect(w, r, "/auth/users?error=database", http.StatusSeeOther)
		return
	}
	var wasAdmin bool
	if err = tx.QueryRow(ctx, `SELECT is_admin FROM portal_users WHERE id=$1`, id).Scan(&wasAdmin); err != nil {
		http.Redirect(w, r, "/auth/users?error=notfound", http.StatusSeeOther)
		return
	}
	if wasAdmin {
		var adminCount int
		if err = tx.QueryRow(ctx, `SELECT COUNT(*) FROM portal_users WHERE is_admin`).Scan(&adminCount); err != nil {
			log.Printf("auth count admins: %v", err)
			http.Redirect(w, r, "/auth/users?error=database", http.StatusSeeOther)
			return
		}
		if adminCount <= 1 {
			http.Redirect(w, r, "/auth/users?error=lastadmin", http.StatusSeeOther)
			return
		}
	}
	// Preserve all saved material: notes belong to folders. Transfer those
	// folders and templates before removing the account. Personal drafts will
	// be deleted by ON DELETE CASCADE on note_drafts.user_id.
	for _, q := range []string{
		`UPDATE folders SET owner_id=$1 WHERE owner_id=$2`,
		`UPDATE note_templates SET owner_id=$1 WHERE owner_id=$2`,
	} {
		if _, err = tx.Exec(ctx, q, admin.ID, id); err != nil {
			log.Printf("auth transfer ownership: %v", err)
			http.Redirect(w, r, "/auth/users?error=database", http.StatusSeeOther)
			return
		}
	}
	if _, err = tx.Exec(ctx, `DELETE FROM portal_users WHERE id=$1`, id); err != nil {
		log.Printf("auth delete user: %v", err)
		http.Redirect(w, r, "/auth/users?error=database", http.StatusSeeOther)
		return
	}
	if err = tx.Commit(ctx); err != nil {
		log.Printf("auth delete user commit: %v", err)
		http.Redirect(w, r, "/auth/users?error=database", http.StatusSeeOther)
		return
	}
	http.Redirect(w, r, "/auth/users?deleted=1", http.StatusSeeOther)
}

func requirePortalUser(r *http.Request) (*portalUser, error) {
	u, _ := r.Context().Value(portalUserKey{}).(*portalUser)
	if u == nil {
		return nil, fmt.Errorf("user not found")
	}
	return u, nil
}
