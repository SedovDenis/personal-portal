package main

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"html"
	"html/template"
	"log"
	"net/http"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/jackc/pgx/v5/pgxpool"
)

// Notes module: the old /notes handlers may stay in main.go temporarily.
// Method-specific patterns take precedence over their legacy methodless routes (Go 1.22+).
type notesApp struct {
	db    *pgxpool.Pool
	token string
	tmpl  *template.Template
}

type notesFolder struct {
	ID          int64
	Name        string
	ParentID    *int64
	ParentValue string
	IsFavorite  bool
	IsShared    bool
	Depth       int
	DisplayName string
	URL         string
}

type notesEntry struct {
	ID         int64
	FolderID   int64
	FolderName string
	Title      string
	Content    string
	Preview    string
	CreatedAt  time.Time
	UpdatedAt  time.Time
	IsFavorite bool
	IsShared   bool
}

type notesTemplate struct {
	ID        int64
	Name      string
	Content   string
	Preview   string
	CreatedAt time.Time
	UpdatedAt time.Time
}

type notesPage struct {
	Token           string
	Folders         []notesFolder
	Entries         []notesEntry
	Templates       []notesTemplate
	Current         *notesEntry
	CurrentTemplate *notesTemplate
	IsNew           bool
	IsEditing       bool
	TemplateEditing bool
	TemplateNew     bool
	ShowTemplates   bool
	FolderID        int64
	Search          string
	Filter          string
	Error           string
	Notice          string
	NoteCount       int
	FolderCount     int
}

func registerNotesApp(db *pgxpool.Pool) {
	bytes := make([]byte, 32)
	if _, err := rand.Read(bytes); err != nil {
		log.Fatalf("notes csrf: %v", err)
	}
	t, err := template.New("notes.html").Funcs(template.FuncMap{
		"fmtDate": func(v time.Time) string { return v.In(notesMoscow()).Format("02.01.2006 15:04") },
	}).ParseFiles("templates/notes.html")
	if err != nil {
		log.Fatalf("notes template: %v", err)
	}
	app := &notesApp{db: db, token: base64.RawURLEncoding.EncodeToString(bytes), tmpl: t}

	http.HandleFunc("GET /notes", app.index)
	http.HandleFunc("POST /notes", app.createNote)
	http.HandleFunc("POST /notes/update", app.updateNote)
	http.HandleFunc("POST /notes/delete", app.deleteNote)
	http.HandleFunc("POST /notes/bulk", app.bulkNotes)
	http.HandleFunc("POST /folders", app.createFolder)
	http.HandleFunc("POST /folders/update", app.updateFolder)
	http.HandleFunc("POST /folders/delete", app.deleteFolder)
	http.HandleFunc("POST /notes/templates", app.createTemplate)
	http.HandleFunc("POST /notes/templates/update", app.updateTemplate)
	http.HandleFunc("POST /notes/templates/delete", app.deleteTemplate)
	http.HandleFunc("POST /notes/from-template", app.fromTemplate)
}

func notesMoscow() *time.Location {
	loc, err := time.LoadLocation("Europe/Moscow")
	if err != nil {
		return time.UTC
	}
	return loc
}

func (a *notesApp) verify(w http.ResponseWriter, r *http.Request) bool {
	r.Body = http.MaxBytesReader(w, r.Body, 2<<20)
	if err := r.ParseForm(); err != nil {
		http.Error(w, "Неправильные данные формы", http.StatusBadRequest)
		return false
	}
	if r.PostForm.Get("csrf_token") != a.token {
		http.Error(w, "Страница устарела. Обновите её и повторите действие.", http.StatusForbidden)
		return false
	}
	return true
}

func notesID(value string) (int64, error) {
	id, err := strconv.ParseInt(strings.TrimSpace(value), 10, 64)
	if err != nil || id < 1 {
		return 0, errors.New("некорректный ID")
	}
	return id, nil
}

func notesOptionalID(value string) (*int64, error) {
	if value == "" {
		return nil, nil
	}
	id, err := notesID(value)
	if err != nil {
		return nil, err
	}
	return &id, nil
}

func notesFlag(r *http.Request, name string) bool { return r.PostForm.Get(name) == "on" }
func notesTrim(s string, max int) (string, error) {
	s = strings.TrimSpace(s)
	if s == "" || utf8.RuneCountInString(s) > max {
		return "", fmt.Errorf("длина должна быть от 1 до %d символов", max)
	}
	return s, nil
}

func notesRedirect(w http.ResponseWriter, r *http.Request, query string) {
	http.Redirect(w, r, "/notes"+query, http.StatusSeeOther)
}

func (a *notesApp) getFolders(ctx context.Context) ([]notesFolder, error) {
	rows, err := a.db.Query(ctx, `SELECT id, name, parent_id, is_favorite, is_shared FROM folders ORDER BY lower(name), id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	all := make([]notesFolder, 0)
	for rows.Next() {
		var f notesFolder
		if err := rows.Scan(&f.ID, &f.Name, &f.ParentID, &f.IsFavorite, &f.IsShared); err != nil {
			return nil, err
		}
		if f.ParentID != nil {
			f.ParentValue = strconv.FormatInt(*f.ParentID, 10)
		}
		f.URL = "/notes?folder=" + strconv.FormatInt(f.ID, 10)
		all = append(all, f)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	byParent := make(map[int64][]notesFolder)
	for _, f := range all {
		parent := int64(0)
		if f.ParentID != nil {
			parent = *f.ParentID
		}
		byParent[parent] = append(byParent[parent], f)
	}
	result := make([]notesFolder, 0, len(all))
	visited := map[int64]bool{}
	var walk func(int64, int)
	walk = func(pid int64, depth int) {
		if depth > 30 {
			return
		}
		for _, f := range byParent[pid] {
			if visited[f.ID] {
				continue
			}
			visited[f.ID] = true
			f.Depth = depth
			f.DisplayName = strings.Repeat("— ", depth) + f.Name
			if f.Depth > 7 {
				f.Depth = 7
			}
			result = append(result, f)
			walk(f.ID, depth+1)
		}
	}
	walk(0, 0)
	// Defensive: still show orphaned or cyclic folders in the list.
	for _, f := range all {
		if !visited[f.ID] {
			f.Depth = 0
			f.DisplayName = f.Name
			result = append(result, f)
		}
	}
	return result, nil
}

var notesTagRe = regexp.MustCompile(`<[^>]*>`)

func notesPreview(s string) string {
	s = notesTagRe.ReplaceAllString(s, " ")
	s = strings.Join(strings.Fields(html.UnescapeString(s)), " ")
	if utf8.RuneCountInString(s) > 115 {
		return string([]rune(s)[:115]) + "…"
	}
	return s
}

func (a *notesApp) getEntries(ctx context.Context, folderID int64, search, filter string) ([]notesEntry, error) {
	// Filters are bound parameters; folder filter includes nested folders.
	query := `SELECT n.id, n.folder_id, f.name, n.title, n.content, n.created_at, n.updated_at,
              f.is_favorite, f.is_shared
              FROM notes n JOIN folders f ON f.id=n.folder_id
              WHERE ($1::bigint=0 OR n.folder_id IN (
                  WITH RECURSIVE folder_tree AS (
                      SELECT id FROM folders WHERE id=$1
                      UNION
                      SELECT child.id FROM folders child JOIN folder_tree ft ON child.parent_id=ft.id
                  ) SELECT id FROM folder_tree
              ))
              AND ($2::text='' OR n.title ILIKE '%'||$2||'%' OR n.content ILIKE '%'||$2||'%')
              AND ($3::text='all' OR ($3='favorite' AND f.is_favorite) OR
                   ($3='shared' AND f.is_shared))
              ORDER BY n.updated_at DESC, n.id DESC`
	rows, err := a.db.Query(ctx, query, folderID, search, filter)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	notes := make([]notesEntry, 0)
	for rows.Next() {
		var n notesEntry
		if err := rows.Scan(&n.ID, &n.FolderID, &n.FolderName, &n.Title, &n.Content, &n.CreatedAt, &n.UpdatedAt, &n.IsFavorite, &n.IsShared); err != nil {
			return nil, err
		}
		n.Preview = notesPreview(n.Content)
		notes = append(notes, n)
	}
	return notes, rows.Err()
}
func (a *notesApp) getTemplates(ctx context.Context) ([]notesTemplate, error) {
	rows, err := a.db.Query(ctx, `SELECT id, name, content, created_at, updated_at FROM note_templates ORDER BY lower(name), id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var items []notesTemplate
	for rows.Next() {
		var t notesTemplate
		if err := rows.Scan(&t.ID, &t.Name, &t.Content, &t.CreatedAt, &t.UpdatedAt); err != nil {
			return nil, err
		}
		t.Preview = notesPreview(t.Content)
		items = append(items, t)
	}
	return items, rows.Err()
}
func (a *notesApp) index(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	folders, err := a.getFolders(ctx)
	if err != nil {
		a.fail(w, "получение папок", err)
		return
	}
	page := notesPage{Token: a.token, Folders: folders, FolderCount: len(folders), Filter: r.URL.Query().Get("filter"), Search: strings.TrimSpace(r.URL.Query().Get("q"))}
	if page.Filter != "favorite" && page.Filter != "shared" {
		page.Filter = "all"
	}
	if len(page.Search) > 100 {
		page.Search = page.Search[:100]
	}
	if r.URL.Query().Has("folder") {
		if page.FolderID, err = notesID(r.URL.Query().Get("folder")); err != nil {
			http.Error(w, "Некорректная папка", 400)
			return
		}
	}
	page.ShowTemplates = r.URL.Query().Get("view") == "templates"
	page.IsNew = r.URL.Query().Get("new") == "1"
	page.TemplateNew = r.URL.Query().Get("new_template") == "1"
	page.Notice = map[string]string{"folder_saved": "Папка сохранена", "folder_removed": "Папка удалена", "note_saved": "Заметка сохранена", "note_removed": "Заметки удалены", "template_saved": "Шаблон сохранён", "template_removed": "Шаблон удалён", "note_moved": "Заметки перемещены"}[r.URL.Query().Get("ok")]
	page.Error = map[string]string{"folder_not_empty": "Сначала переместите или удалите все заметки и вложенные папки", "folder_cycle": "Нельзя перемещать папку внутрь самой себя", "folder_invalid": "Родительская папка не найдена", "note_not_found": "Заметка не найдена", "select_notes": "Выберите хотя бы одну заметку", "folder_required": "Сначала создайте папку"}[r.URL.Query().Get("error")]
	if page.ShowTemplates {
		page.Templates, err = a.getTemplates(ctx)
		if err != nil {
			a.fail(w, "получение шаблонов", err)
			return
		}
		if idStr := r.URL.Query().Get("template"); idStr != "" {
			id, parseErr := notesID(idStr)
			if parseErr != nil {
				http.Error(w, "Некорректный ID", 400)
				return
			}
			for i := range page.Templates {
				if page.Templates[i].ID == id {
					page.CurrentTemplate = &page.Templates[i]
					break
				}
			}
		}
		page.TemplateEditing = page.CurrentTemplate != nil && r.URL.Query().Get("edit_template") == "1"
	} else {
		page.Entries, err = a.getEntries(ctx, page.FolderID, page.Search, page.Filter)
		if err != nil {
			a.fail(w, "получение заметок", err)
			return
		}
		page.NoteCount = len(page.Entries)
		if idStr := r.URL.Query().Get("note"); idStr != "" {
			id, parseErr := notesID(idStr)
			if parseErr != nil {
				http.Error(w, "Некорректный ID", 400)
				return
			}
			for i := range page.Entries {
				if page.Entries[i].ID == id {
					page.Current = &page.Entries[i]
					break
				}
			}
			if page.Current == nil {
				page.Error = "Заметка не найдена в текущей выборке"
			}
		}
		page.IsEditing = page.Current != nil && r.URL.Query().Get("edit") == "1"
		if page.IsNew && page.FolderID == 0 && len(folders) > 0 {
			page.FolderID = folders[0].ID
		}
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Referrer-Policy", "same-origin")
	if err := a.tmpl.ExecuteTemplate(w, "notes.html", page); err != nil {
		log.Printf("notes template: %v", err)
	}
}
func (a *notesApp) fail(w http.ResponseWriter, operation string, err error) {
	log.Printf("notes: %s: %v", operation, err)
	http.Error(w, "Ошибка раздела заметок", http.StatusInternalServerError)
}
func (a *notesApp) folderExists(ctx context.Context, id int64) bool {
	var check int64
	return a.db.QueryRow(ctx, `SELECT id FROM folders WHERE id=$1`, id).Scan(&check) == nil
}
func (a *notesApp) createFolder(w http.ResponseWriter, r *http.Request) {
	if !a.verify(w, r) {
		return
	}
	name, err := notesTrim(r.FormValue("name"), 120)
	if err != nil {
		http.Error(w, "Укажите название папки (до 120 символов)", 400)
		return
	}
	parent, err := notesOptionalID(r.FormValue("parent_id"))
	if err != nil {
		http.Error(w, "Неверный ID родителя", 400)
		return
	}
	if parent != nil && !a.folderExists(r.Context(), *parent) {
		notesRedirect(w, r, "?error=folder_invalid")
		return
	}
	_, err = a.db.Exec(r.Context(), `INSERT INTO folders (name,parent_id,is_favorite,is_shared) VALUES ($1,$2,$3,$4)`, name, parent, notesFlag(r, "is_favorite"), notesFlag(r, "is_shared"))
	if err != nil {
		a.fail(w, "создание папки", err)
		return
	}
	notesRedirect(w, r, "?ok=folder_saved")
}
func (a *notesApp) wouldCycle(ctx context.Context, id int64, parent *int64) (bool, error) {
	if parent == nil {
		return false, nil
	}
	var isAncestor bool
	err := a.db.QueryRow(ctx, `WITH RECURSIVE parents AS (
        SELECT id,parent_id FROM folders WHERE id=$1
        UNION SELECT f.id,f.parent_id FROM folders f JOIN parents p ON f.id=p.parent_id
    ) SELECT EXISTS(SELECT 1 FROM parents WHERE id=$2)`, *parent, id).Scan(&isAncestor)
	return isAncestor, err
}
func (a *notesApp) updateFolder(w http.ResponseWriter, r *http.Request) {
	if !a.verify(w, r) {
		return
	}
	id, err := notesID(r.FormValue("id"))
	if err != nil {
		http.Error(w, "Неверный ID папки", 400)
		return
	}
	name, err := notesTrim(r.FormValue("name"), 120)
	if err != nil {
		http.Error(w, "Неверное название", 400)
		return
	}
	parent, err := notesOptionalID(r.FormValue("parent_id"))
	if err != nil {
		http.Error(w, "Неверный родитель", 400)
		return
	}
	if parent != nil && !a.folderExists(r.Context(), *parent) {
		notesRedirect(w, r, "?error=folder_invalid")
		return
	}
	cycle, err := a.wouldCycle(r.Context(), id, parent)
	if err != nil {
		a.fail(w, "проверка папки", err)
		return
	}
	if cycle {
		notesRedirect(w, r, "?error=folder_cycle")
		return
	}
	result, err := a.db.Exec(r.Context(), `UPDATE folders SET name=$1,parent_id=$2,is_favorite=$3,is_shared=$4,updated_at=NOW() WHERE id=$5`, name, parent, notesFlag(r, "is_favorite"), notesFlag(r, "is_shared"), id)
	if err != nil {
		a.fail(w, "изменение папки", err)
		return
	}
	if result.RowsAffected() == 0 {
		http.Error(w, "Папка не найдена", 404)
		return
	}
	notesRedirect(w, r, "?ok=folder_saved")
}
func (a *notesApp) deleteFolder(w http.ResponseWriter, r *http.Request) {
	if !a.verify(w, r) {
		return
	}
	if r.FormValue("confirm") != "yes" {
		http.Error(w, "Не подтверждено удаление", 400)
		return
	}
	id, err := notesID(r.FormValue("id"))
	if err != nil {
		http.Error(w, "Неверный ID", 400)
		return
	}
	var n int
	err = a.db.QueryRow(r.Context(), `SELECT (SELECT count(*) FROM notes WHERE folder_id=$1)+(SELECT count(*) FROM folders WHERE parent_id=$1)`, id).Scan(&n)
	if err != nil {
		a.fail(w, "проверка содержимого папки", err)
		return
	}
	if n > 0 {
		notesRedirect(w, r, "?error=folder_not_empty")
		return
	}
	_, err = a.db.Exec(r.Context(), `DELETE FROM folders WHERE id=$1`, id)
	if err != nil {
		notesRedirect(w, r, "?error=folder_not_empty")
		return
	} // FK is the final safeguard.
	notesRedirect(w, r, "?ok=folder_removed")
}
func (a *notesApp) createNote(w http.ResponseWriter, r *http.Request) {
	if !a.verify(w, r) {
		return
	}
	title, err := notesTrim(r.FormValue("title"), 200)
	if err != nil {
		http.Error(w, "Укажите название заметки", 400)
		return
	}
	folderID, err := notesID(r.FormValue("folder_id"))
	if err != nil || !a.folderExists(r.Context(), folderID) {
		http.Error(w, "Выберите существующую папку", 400)
		return
	}
	content := r.FormValue("content")
	if len(content) > 500_000 {
		http.Error(w, "Заметка слишком большая", 400)
		return
	}
	var id int64
	err = a.db.QueryRow(r.Context(), `INSERT INTO notes (title,content,folder_id) VALUES ($1,$2,$3) RETURNING id`, title, content, folderID).Scan(&id)
	if err != nil {
		a.fail(w, "создание заметки", err)
		return
	}
	notesRedirect(w, r, "?note="+strconv.FormatInt(id, 10)+"&ok=note_saved")
}
func (a *notesApp) updateNote(w http.ResponseWriter, r *http.Request) {
	if !a.verify(w, r) {
		return
	}
	id, err := notesID(r.FormValue("id"))
	if err != nil {
		http.Error(w, "Неверный ID", 400)
		return
	}
	title, err := notesTrim(r.FormValue("title"), 200)
	if err != nil {
		http.Error(w, "Укажите название заметки", 400)
		return
	}
	folderID, err := notesID(r.FormValue("folder_id"))
	if err != nil || !a.folderExists(r.Context(), folderID) {
		http.Error(w, "Выберите существующую папку", 400)
		return
	}
	content := r.FormValue("content")
	if len(content) > 500_000 {
		http.Error(w, "Заметка слишком большая", 400)
		return
	}
	result, err := a.db.Exec(r.Context(), `UPDATE notes SET title=$1,content=$2,folder_id=$3,updated_at=NOW() WHERE id=$4`, title, content, folderID, id)
	if err != nil {
		a.fail(w, "обновление заметки", err)
		return
	}
	if result.RowsAffected() == 0 {
		http.Error(w, "Заметка не найдена", 404)
		return
	}
	notesRedirect(w, r, "?note="+strconv.FormatInt(id, 10)+"&ok=note_saved")
}
func (a *notesApp) deleteNote(w http.ResponseWriter, r *http.Request) {
	if !a.verify(w, r) {
		return
	}
	if r.FormValue("confirm") != "yes" {
		http.Error(w, "Не подтверждено удаление", 400)
		return
	}
	id, err := notesID(r.FormValue("id"))
	if err != nil {
		http.Error(w, "Неверный ID", 400)
		return
	}
	_, err = a.db.Exec(r.Context(), `DELETE FROM notes WHERE id=$1`, id)
	if err != nil {
		a.fail(w, "удаление заметки", err)
		return
	}
	notesRedirect(w, r, "?ok=note_removed")
}
func notesSelected(values []string) ([]int64, error) {
	unique := make(map[int64]bool)
	ids := make([]int64, 0, len(values))

	for _, v := range values {
		id, err := notesID(v)
		if err != nil {
			return nil, err
		}
		if !unique[id] {
			unique[id] = true
			ids = append(ids, id)
		}
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	return ids, nil
}
func (a *notesApp) bulkNotes(w http.ResponseWriter, r *http.Request) {
	if !a.verify(w, r) {
		return
	}
	ids, err := notesSelected(r.PostForm["ids"])
	if err != nil {
		http.Error(w, "Неверный список заметок", 400)
		return
	}
	if len(ids) == 0 {
		notesRedirect(w, r, "?error=select_notes")
		return
	}
	switch r.FormValue("operation") {
	case "delete":
		if r.FormValue("confirm") != "yes" {
			http.Error(w, "Не подтверждено удаление", 400)
			return
		}
		_, err = a.db.Exec(r.Context(), `DELETE FROM notes WHERE id=ANY($1::bigint[])`, ids)
		if err != nil {
			a.fail(w, "массовое удаление", err)
			return
		}
		notesRedirect(w, r, "?ok=note_removed")
	case "move":
		folder, err := notesID(r.FormValue("folder_id"))
		if err != nil || !a.folderExists(r.Context(), folder) {
			http.Error(w, "Выберите папку", 400)
			return
		}
		_, err = a.db.Exec(r.Context(), `UPDATE notes SET folder_id=$1,updated_at=NOW() WHERE id=ANY($2::bigint[])`, folder, ids)
		if err != nil {
			a.fail(w, "перемещение заметок", err)
			return
		}
		notesRedirect(w, r, "?folder="+strconv.FormatInt(folder, 10)+"&ok=note_moved")
	default:
		http.Error(w, "Неизвестная операция", 400)
	}
}
func (a *notesApp) createTemplate(w http.ResponseWriter, r *http.Request) {
	if !a.verify(w, r) {
		return
	}
	name, err := notesTrim(r.FormValue("name"), 150)
	if err != nil {
		http.Error(w, "Укажите название шаблона", 400)
		return
	}
	content := r.FormValue("content")
	if len(content) > 500_000 {
		http.Error(w, "Шаблон слишком большой", 400)
		return
	}
	var id int64
	err = a.db.QueryRow(r.Context(), `INSERT INTO note_templates(name,content) VALUES($1,$2) RETURNING id`, name, content).Scan(&id)
	if err != nil {
		a.fail(w, "создание шаблона", err)
		return
	}
	notesRedirect(w, r, "?view=templates&template="+strconv.FormatInt(id, 10)+"&ok=template_saved")
}
func (a *notesApp) updateTemplate(w http.ResponseWriter, r *http.Request) {
	if !a.verify(w, r) {
		return
	}
	id, err := notesID(r.FormValue("id"))
	if err != nil {
		http.Error(w, "Неверный ID", 400)
		return
	}
	name, err := notesTrim(r.FormValue("name"), 150)
	if err != nil {
		http.Error(w, "Укажите название шаблона", 400)
		return
	}
	content := r.FormValue("content")
	if len(content) > 500_000 {
		http.Error(w, "Шаблон слишком большой", 400)
		return
	}
	result, err := a.db.Exec(r.Context(), `UPDATE note_templates SET name=$1,content=$2,updated_at=NOW() WHERE id=$3`, name, content, id)
	if err != nil {
		a.fail(w, "обновление шаблона", err)
		return
	}
	if result.RowsAffected() == 0 {
		http.Error(w, "Шаблон не найден", 404)
		return
	}
	notesRedirect(w, r, "?view=templates&template="+strconv.FormatInt(id, 10)+"&ok=template_saved")
}
func (a *notesApp) deleteTemplate(w http.ResponseWriter, r *http.Request) {
	if !a.verify(w, r) {
		return
	}
	if r.FormValue("confirm") != "yes" {
		http.Error(w, "Не подтверждено удаление", 400)
		return
	}
	id, err := notesID(r.FormValue("id"))
	if err != nil {
		http.Error(w, "Неверный ID", 400)
		return
	}
	_, err = a.db.Exec(r.Context(), `DELETE FROM note_templates WHERE id=$1`, id)
	if err != nil {
		a.fail(w, "удаление шаблона", err)
		return
	}
	notesRedirect(w, r, "?view=templates&ok=template_removed")
}
func (a *notesApp) fromTemplate(w http.ResponseWriter, r *http.Request) {
	if !a.verify(w, r) {
		return
	}
	templateID, err := notesID(r.FormValue("template_id"))
	if err != nil {
		http.Error(w, "Неверный шаблон", 400)
		return
	}
	folderID, err := notesID(r.FormValue("folder_id"))
	if err != nil || !a.folderExists(r.Context(), folderID) {
		http.Error(w, "Выберите папку", 400)
		return
	}
	var id int64
	err = a.db.QueryRow(r.Context(), `INSERT INTO notes(title,content,folder_id) SELECT name,content,$1 FROM note_templates WHERE id=$2 RETURNING id`, folderID, templateID).Scan(&id)
	if err != nil {
		a.fail(w, "создание заметки из шаблона", err)
		return
	}
	notesRedirect(w, r, "?note="+strconv.FormatInt(id, 10)+"&ok=note_saved")
}
