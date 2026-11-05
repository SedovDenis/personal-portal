package main

import "testing"

func TestDraftLink(t *testing.T) {
	cases := []struct {
		kind string
		id   int64
		want string
	}{
		{"note", 0, "/notes?new=1"},
		{"note", 5, "/notes?note=5&edit=1"},
		{"template", 0, "/notes?view=templates&new_template=1"},
		{"template", 4, "/notes?view=templates&template=4&edit_template=1"},
	}
	for _, c := range cases {
		if got := draftLink(c.kind, c.id); got != c.want {
			t.Fatalf("draftLink=%s, want=%s", got, c.want)
		}
	}
}
