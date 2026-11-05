package main

import (
	"bytes"
	"html/template"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestAuthRedirectAndPublicHealth(t *testing.T) {
	auth := &portalAuth{}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /notes", func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusOK) })
	mux.HandleFunc("GET /health", func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusOK) })
	h := auth.middleware(mux)
	test := func(path string, want int) {
		t.Helper()
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest("GET", path, nil))
		if w.Code != want {
			t.Fatalf("GET %s status=%d want=%d", path, w.Code, want)
		}
	}
	test("http://example.test/health", 200)
	test("http://example.test/notes", 303)
	post := httptest.NewRecorder()
	h.ServeHTTP(post, httptest.NewRequest("POST", "http://example.test/notes", strings.NewReader("test")))
	if post.Code != 401 {
		t.Fatalf("unauthenticated POST status=%d", post.Code)
	}
}
func TestAuthRedirectPathSafety(t *testing.T) {
	cases := []struct{ in, out string }{
		{"https://evil.example/path", "/"},
		{"//evil.example/path", "/"},
		{"/notes?view=templates", "/notes?view=templates"},
		{"/notes\\foo", "/"},
	}
	for _, c := range cases {
		if got := validNext(c.in); got != c.out {
			t.Fatalf("%q -> %q expected %q", c.in, got, c.out)
		}
	}
}

func TestPortalUserDetails(t *testing.T) {
	cases := []struct {
		name, display string
		valid         bool
	}{
		{"admin", "Администратор", true},
		{"john.doe", "Иван Петров", true},
		{"ab", "Иван", false},
		{"in valid", "Иван", false},
		{"valid_user", "", false},
		{"valid_user", strings.Repeat("Я", 121), false},
	}
	for _, c := range cases {
		if got := validPortalUserDetails(c.name, c.display); got != c.valid {
			t.Errorf("validPortalUserDetails(%q, %q) = %t", c.name, c.display, got)
		}
	}
}

func TestUserManagementPermissions(t *testing.T) {
	auth := &portalAuth{}
	mux := http.NewServeMux()
	mux.HandleFunc("POST /auth/users/update", auth.updateUser)
	mux.HandleFunc("POST /auth/users/delete", auth.deleteUser)
	for _, path := range []string{"/auth/users/update", "/auth/users/delete"} {
		request := httptest.NewRequest("POST", path, strings.NewReader("id=2"))
		request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		res := httptest.NewRecorder()
		mux.ServeHTTP(res, request)
		if res.Code != http.StatusForbidden {
			t.Errorf("%s without admin: got %d, expected 403", path, res.Code)
		}
	}
}

func TestUsersTemplateRendersManagementForms(t *testing.T) {
	tmpl, err := template.ParseFiles("templates/users.html", "templates/portal-nav.html")
	if err != nil {
		t.Fatalf("parse users template: %v", err)
	}
	data := struct {
		Nav            portalNav
		Current        *portalUser
		Users          []portalUser
		Token, Message string
	}{
		Nav:     newPortalNav(&portalUser{Username: "admin", DisplayName: "Администратор", IsAdmin: true}, "test-csrf", "users"),
		Current: &portalUser{ID: 1, Username: "admin", DisplayName: "Администратор", IsAdmin: true},
		Users: []portalUser{
			{ID: 1, Username: "admin", DisplayName: "Администратор", IsAdmin: true},
			{ID: 2, Username: "demo", DisplayName: "Тест", IsAdmin: false},
		},
		Token: "test-csrf",
	}
	var out bytes.Buffer
	if err := tmpl.Execute(&out, data); err != nil {
		t.Fatalf("execute users template: %v", err)
	}
	text := out.String()
	for _, fragment := range []string{`action="/auth/users/update"`, `action="/auth/users/delete"`, `name="csrf_token"`, `data-confirm=`} {
		if !strings.Contains(text, fragment) {
			t.Errorf("missing %q in users HTML", fragment)
		}
	}
	if got := strings.Count(text, `action="/auth/users/delete"`); got != 1 {
		t.Errorf("got %d delete forms, expected 1 (non-self only)", got)
	}
}
