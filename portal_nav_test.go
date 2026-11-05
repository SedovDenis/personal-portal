package main

import (
	"bytes"
	"html/template"
	"strings"
	"testing"
)

func TestPortalNavRoleAndSelection(t *testing.T) {
	u := &portalUser{ID: 7, Username: "denis", DisplayName: "Денис", IsAdmin: false}
	nav := newPortalNav(u, "test-csrf", "notes")
	if nav.Username != "denis" || nav.IsAdmin || nav.CSRFToken != "test-csrf" || nav.Active != "notes" {
		t.Fatalf("unexpected nav: %#v", nav)
	}
	tmpl, err := template.ParseFiles("templates/portal-nav.html")
	if err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	if err := tmpl.ExecuteTemplate(&buf, "portal-header", nav); err != nil {
		t.Fatal(err)
	}
	html := buf.String()
	if !strings.Contains(html, `aria-current="page"`) || !strings.Contains(html, `action="/auth/logout"`) {
		t.Fatal("missing links or logout form")
	}
	if strings.Contains(html, "/auth/users") {
		t.Fatal("admin link visible to regular user")
	}
	if strings.Contains(html, "/mortgage") {
		t.Fatal("obsolete module is linked")
	}
}

func TestPortalNavAdmin(t *testing.T) {
	nav := newPortalNav(&portalUser{Username: "admin", DisplayName: "Администратор", IsAdmin: true}, "csrf", "dashboard")
	tmpl, err := template.ParseFiles("templates/portal-nav.html")
	if err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	if err := tmpl.ExecuteTemplate(&buf, "portal-header", nav); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(buf.String(), "/auth/users") {
		t.Fatal("admin menu is missing")
	}
}
