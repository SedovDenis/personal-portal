// Package authhash implements PBKDF2-HMAC-SHA256 password storage without external dependencies.
// Never use a fast hash such as SHA256 alone for passwords.
package authhash

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"strconv"
	"strings"
)

const iterations = 600000

func derive(password, salt []byte, count int) []byte {
	// RFC 8018 PBKDF2, 32-byte output = one SHA256 block.
	var block [4]byte
	block[3] = 1
	mac := hmac.New(sha256.New, password)
	mac.Write(salt)
	mac.Write(block[:])
	u := mac.Sum(nil)
	result := append([]byte(nil), u...)
	for i := 1; i < count; i++ {
		mac.Reset()
		mac.Write(u)
		u = mac.Sum(u[:0])
		for j := range result {
			result[j] ^= u[j]
		}
	}
	return result
}

func Hash(password string) (string, error) {
	if len(password) < 12 || len(password) > 256 {
		return "", errors.New("пароль должен содержать от 12 до 256 байт")
	}
	salt := make([]byte, 16)
	if _, err := rand.Read(salt); err != nil {
		return "", err
	}
	hash := derive([]byte(password), salt, iterations)
	return fmt.Sprintf("pbkdf2-sha256$%d$%s$%s", iterations,
		base64.RawStdEncoding.EncodeToString(salt), base64.RawStdEncoding.EncodeToString(hash)), nil
}

func Verify(password, stored string) bool {
	parts := strings.Split(stored, "$")
	if len(parts) != 4 || parts[0] != "pbkdf2-sha256" {
		return false
	}
	count, err := strconv.Atoi(parts[1])
	if err != nil || count < 100000 || count > 1000000 {
		return false
	}
	salt, e1 := base64.RawStdEncoding.DecodeString(parts[2])
	wanted, e2 := base64.RawStdEncoding.DecodeString(parts[3])
	if e1 != nil || e2 != nil || len(salt) != 16 || len(wanted) != 32 {
		return false
	}
	got := derive([]byte(password), salt, count)
	return subtle.ConstantTimeCompare(got, wanted) == 1
}
