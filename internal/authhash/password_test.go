package authhash

import (
	"fmt"
	"testing"
)

func TestHashVerify(t *testing.T) {
	h, err := Hash("my-very-strong-password")
	if err != nil {
		t.Fatal(err)
	}
	if !Verify("my-very-strong-password", h) || Verify("wrong password", h) {
		t.Fatal("password verification failed")
	}
	if _, err := Hash("short"); err == nil {
		t.Fatal("short password accepted")
	}
}
func TestPBKDF2Vectors(t *testing.T) {
	cases := []struct {
		count    int
		expected string
	}{
		{1, "120fb6cffcf8b32c43e7225256c4f837a86548c92ccc35480805987cb70be17b"},
		{2, "ae4d0c95af6b46d32d0adff928f06dd02a303f8ef3c251dfd6e2d85a95474c43"},
	}
	for _, c := range cases {
		got := derive([]byte("password"), []byte("salt"), c.count)
		if fmt.Sprintf("%x", got) != c.expected {
			t.Fatalf("PBKDF2(%d) mismatch: %x", c.count, got)
		}
	}
}
