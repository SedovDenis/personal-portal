package main

import (
	"context"
	"errors"
	"fmt"
	"github.com/jackc/pgx/v5"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"
)

type noteDraft struct {
	Kind        string
	TargetID    int64
	Title       string
	FolderID    *int64
	FolderValue string
	Content     string
	UpdatedAt   time.Time
	URL         string
	Description string
}

func draftLink(kind string, target int64) string {
	if kind == "template" {
		if target == 0 {
			return "/notes?view=templates&new_template=1"
		}
		return "/notes?view=templates&template=" + strconv.FormatInt(target, 10) + "&edit_template=1"
	}
	if target == 0 {
		return "/notes?new=1"
	}
	return "/notes?note=" + strconv.FormatInt(target, 10) + "&edit=1"
}
func (a *notesApp) getDrafts(ctx context.Context, uid int64) ([]noteDraft, error) {
	rows, err := a.db.Query(ctx, `SELECT d.kind,d.target_id,d.title,d.folder_id,d.content,d.updated_at
 FROM note_drafts d WHERE d.user_id=$1 AND (
 d.target_id=0 OR (d.kind='note' AND EXISTS (
 SELECT 1 FROM notes n WHERE n.id=d.target_id AND notes_can_access_folder(n.folder_id,$1)
 )) OR (d.kind='template' AND EXISTS (
 SELECT 1 FROM note_templates t WHERE t.id=d.target_id AND t.owner_id=$1
 ))) ORDER BY d.updated_at DESC`, uid)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	drafts := []noteDraft{}
	for rows.Next() {
		var d noteDraft
		if err := rows.Scan(&d.Kind, &d.TargetID, &d.Title, &d.FolderID, &d.Content, &d.UpdatedAt); err != nil {
			return nil, err
		}
		if d.FolderID != nil {
			d.FolderValue = strconv.FormatInt(*d.FolderID, 10)
		}
		d.URL = draftLink(d.Kind, d.TargetID)
		if strings.TrimSpace(d.Title) == "" {
			d.Title = "Без названия"
		}
		if d.Kind == "template" {
			d.Description = "Шаблон"
		} else {
			d.Description = "Заметка"
		}
		drafts = append(drafts, d)
	}
	return drafts, rows.Err()
}
func (a *notesApp) getDraft(ctx context.Context, uid int64, kind string, target int64) (*noteDraft, error) {
	var d noteDraft
	err := a.db.QueryRow(ctx, `SELECT kind,target_id,title,folder_id,content,updated_at FROM note_drafts WHERE user_id=$1 AND kind=$2 AND target_id=$3`, uid, kind, target).Scan(&d.Kind, &d.TargetID, &d.Title, &d.FolderID, &d.Content, &d.UpdatedAt)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	if d.FolderID != nil {
		d.FolderValue = strconv.FormatInt(*d.FolderID, 10)
	}
	return &d, nil
}
func (a *notesApp) clearDraft(ctx context.Context, uid int64, kind string, target int64) {
	if _, err := a.db.Exec(ctx, `DELETE FROM note_drafts WHERE user_id=$1 AND kind=$2 AND target_id=$3`, uid, kind, target); err != nil {
		log.Printf("notes draft cleanup: %v", err)
	}
}
func (a *notesApp) saveDraft(w http.ResponseWriter, r *http.Request) {
	if !a.verify(w, r) {
		return
	}
	u := a.auth.userFromRequest(r)
	kind := r.FormValue("kind")
	if kind != "note" && kind != "template" {
		http.Error(w, "Некорректный тип черновика", 400)
		return
	}
	targetStr := strings.TrimSpace(r.FormValue("target_id"))
	target := int64(0)
	if targetStr != "" {
		id, err := strconv.ParseInt(targetStr, 10, 64)
		if err != nil || id < 0 {
			http.Error(w, "Неверная запись", 400)
			return
		}
		target = id
	}
	if target > 0 {
		if kind == "note" && !a.canEditNote(r.Context(), target, u.ID) {
			http.Error(w, "Нет доступа к заметке", 403)
			return
		}
		if kind == "template" {
			var check int64
			if a.db.QueryRow(r.Context(), `SELECT id FROM note_templates WHERE id=$1 AND owner_id=$2`, target, u.ID).Scan(&check) != nil {
				http.Error(w, "Нет доступа к шаблону", 403)
				return
			}
		}
	}
	title := strings.TrimSpace(r.FormValue("title"))
	if len(title) > 300 {
		http.Error(w, "Слишком длинное название", 400)
		return
	}
	content := r.FormValue("content")
	if len(content) > 500000 {
		http.Error(w, "Слишком большой черновик", 400)
		return
	}
	var folder *int64
	if kind == "note" && r.FormValue("folder_id") != "" {
		id, err := notesID(r.FormValue("folder_id"))
		if err != nil || !a.folderExists(r.Context(), id, u.ID) {
			http.Error(w, "Нет доступа к папке", 403)
			return
		}
		folder = &id
	}
	_, err := a.db.Exec(r.Context(), `INSERT INTO note_drafts(user_id,kind,target_id,title,folder_id,content,updated_at)
 VALUES($1,$2,$3,$4,$5,$6,NOW()) ON CONFLICT(user_id,kind,target_id)
 DO UPDATE SET title=EXCLUDED.title,folder_id=EXCLUDED.folder_id,content=EXCLUDED.content,updated_at=NOW()`, u.ID, kind, target, title, folder, content)
	if err != nil {
		log.Printf("notes draft save: %v", err)
		http.Error(w, "Не удалось сохранить черновик", 500)
		return
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	fmt.Fprint(w, `{"ok":true}`)
}
func (a *notesApp) discardDraft(w http.ResponseWriter, r *http.Request) {
	if !a.verify(w, r) {
		return
	}
	if r.FormValue("confirm") != "yes" {
		http.Error(w, "Подтвердите удаление", 400)
		return
	}
	kind := r.FormValue("kind")
	if kind != "note" && kind != "template" {
		http.Error(w, "Неверный тип", 400)
		return
	}
	id, err := strconv.ParseInt(r.FormValue("target_id"), 10, 64)
	if err != nil || id < 0 {
		http.Error(w, "Неверный идентификатор", 400)
		return
	}
	a.clearDraft(r.Context(), a.auth.userFromRequest(r).ID, kind, id)
	http.Redirect(w, r, "/notes", http.StatusSeeOther)
}
