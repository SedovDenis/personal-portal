package main

import (
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
		{"https://evil.example/path", "/notes"},
		{"//evil.example/path", "/notes"},
		{"/notes?view=templates", "/notes?view=templates"},
		{"/notes\\foo", "/notes"},
	}
	for _, c := range cases {
		if got := validNext(c.in); got != c.out {
			t.Fatalf("%q -> %q expected %q", c.in, got, c.out)
		}
	}
}
