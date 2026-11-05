package main

import (
	"bytes"
	"html/template"
	"strings"
	"testing"
	"time"
)

func TestNotesFormIdentifiers(t *testing.T) {
	if id, err := notesID("12"); err != nil || id != 12 {
		t.Fatal(id, err)
	}
	if _, err := notesID("0"); err == nil {
		t.Fatal("ID 0 must be rejected")
	}
	if id, err := notesOptionalID(""); err != nil || id != nil {
		t.Fatal(id, err)
	}
}
func TestNotesSelectedIDs(t *testing.T) {
	got, err := notesSelected([]string{"3", "2", "3"})
	if err != nil || len(got) != 2 || got[0] != 2 || got[1] != 3 {
		t.Fatal(got, err)
	}
	if _, err := notesSelected([]string{"oops"}); err == nil {
		t.Fatal("invalid id accepted")
	}
}
func TestNotesPreview(t *testing.T) {
	got := notesPreview("<p>Купить <b>молоко</b> &amp; хлеб</p>")
	if got != "Купить молоко & хлеб" {
		t.Fatal(got)
	}
}
func TestNotesTemplateRenders(t *testing.T) {
	tmpl, err := template.New("notes.html").Funcs(template.FuncMap{
		"fmtDate": func(v time.Time) string { return v.Format("02.01.2006 15:04") },
	}).ParseFiles("templates/notes.html")
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	folderID := int64(1)
	cases := []notesPage{
		{Token: "abc", Folders: []notesFolder{{ID: folderID, Name: "Работа", IsFavorite: true}}, Filter: "all", IsNew: true, FolderID: folderID},
		{Token: "abc", Folders: []notesFolder{{ID: folderID, Name: "Работа"}}, Entries: []notesEntry{{ID: 1, FolderID: folderID, FolderName: "Работа", Title: "Тестовая", CreatedAt: now, UpdatedAt: now}}, Filter: "all"},
		{Token: "abc", ShowTemplates: true, Templates: []notesTemplate{{ID: 1, Name: "Чек-лист", CreatedAt: now, UpdatedAt: now}}, CurrentTemplate: &notesTemplate{ID: 1, Name: "Чек-лист", CreatedAt: now, UpdatedAt: now}},
		{Token: "abc", ShowTemplates: true, TemplateNew: true},
	}
	for i, p := range cases {
		var out bytes.Buffer
		if err := tmpl.ExecuteTemplate(&out, "notes.html", p); err != nil {
			t.Fatalf("case %d: %v", i, err)
		}
		if !strings.Contains(out.String(), "csrf_token") {
			t.Fatalf("case %d: CSRF token missing", i)
		}
	}
}

func TestNotesEditorOpensOnlyOnExplicitAction(t *testing.T) {
	tmpl, err := template.New("notes.html").Funcs(template.FuncMap{
		"fmtDate": func(v time.Time) string { return v.Format("02.01.2006 15:04") },
	}).ParseFiles("templates/notes.html")
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	note := &notesEntry{ID: 11, FolderID: 3, FolderName: "Работа", Title: "Отчёт", Content: "<p>Текст</p>", CreatedAt: now, UpdatedAt: now}
	tpl := &notesTemplate{ID: 7, Name: "Покупки", Content: "<p>Продукты</p>", CreatedAt: now, UpdatedAt: now}
	cases := []struct {
		name       string
		page       notesPage
		wantEditor bool
		wantView   bool
	}{
		{"initial notes", notesPage{Token: "abc"}, false, false},
		{"note view", notesPage{Token: "abc", Current: note}, false, true},
		{"note edit", notesPage{Token: "abc", Current: note, IsEditing: true}, true, false},
		{"new note", notesPage{Token: "abc", IsNew: true}, true, false},
		{"initial templates", notesPage{Token: "abc", ShowTemplates: true}, false, false},
		{"template view", notesPage{Token: "abc", ShowTemplates: true, CurrentTemplate: tpl}, false, true},
		{"template edit", notesPage{Token: "abc", ShowTemplates: true, CurrentTemplate: tpl, TemplateEditing: true}, true, false},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			var out bytes.Buffer
			if err := tmpl.ExecuteTemplate(&out, "notes.html", test.page); err != nil {
				t.Fatal(err)
			}
			html := out.String()
			if got := strings.Contains(html, `class="rich-note-form"`); got != test.wantEditor {
				t.Fatalf("editor present: %v, want %v", got, test.wantEditor)
			}
			if got := strings.Contains(html, `class="note-readonly-content"`); got != test.wantView {
				t.Fatalf("read-only view present: %v, want %v", got, test.wantView)
			}
			if strings.Contains(html, "Публичные") || strings.Contains(html, `name="is_public"`) {
				t.Fatal("removed public status is still in UI")
			}
		})
	}
}

func TestNotesTemplatesRenderWithAccountAndDraft(t *testing.T) {
	tmpl, err := template.New("notes.html").Funcs(template.FuncMap{"fmtDate": func(v time.Time) string { return v.Format("02.01.2006 15:04") }}).ParseFiles("templates/notes.html")
	if err != nil {
		t.Fatal(err)
	}
	cases := []notesPage{
		{User: &portalUser{ID: 1, DisplayName: "Первый пользователь", IsAdmin: true}, Token: "csrf", Draft: &noteDraft{Title: "Черновик", Content: "<p>Текст</p>"}, IsNew: true},
		{User: &portalUser{ID: 2, DisplayName: "Второй пользователь"}, Token: "csrf", ShowTemplates: true, TemplateNew: true, Draft: &noteDraft{Title: "Шаблон", Content: "<p>Список</p>"}},
	}
	for _, p := range cases {
		var b bytes.Buffer
		if err := tmpl.Execute(&b, p); err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(b.String(), "data-draft-kind=") || !strings.Contains(b.String(), "Черновик") && p.IsNew {
			t.Fatal("draft form not rendered")
		}
		if strings.Contains(b.String(), "Публичные") {
			t.Fatal("public filter reappeared")
		}
	}
}
