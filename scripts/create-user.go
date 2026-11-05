// Usage: read -rsp 'Password: ' PASS; echo; printf '%s\n' "$PASS" | go run scripts/create-user.go admin 'Администратор'; unset PASS
package main

import (
	"bufio"
	"context"
	"fmt"
	"github.com/SedovDenis/personal-portal/internal/authhash"
	"github.com/jackc/pgx/v5/pgxpool"
	"os"
	"regexp"
	"strings"
)

func main() {
	if len(os.Args) != 3 || !regexp.MustCompile(`^[a-zA-Z0-9_.-]{3,40}$`).MatchString(os.Args[1]) {
		fmt.Fprintln(os.Stderr, "Usage: go run scripts/create-user.go USERNAME DISPLAY_NAME")
		os.Exit(2)
	}
	username := strings.ToLower(os.Args[1])
	display := strings.TrimSpace(os.Args[2])
	if display == "" || len(display) > 120 {
		fmt.Fprintln(os.Stderr, "Invalid display name")
		os.Exit(2)
	}
	password, err := bufio.NewReader(os.Stdin).ReadString('\n')
	if err != nil {
		panic(err)
	}
	hash, err := authhash.Hash(strings.TrimRight(password, "\r\n"))
	if err != nil {
		panic(err)
	}
	url := os.Getenv("DATABASE_URL")
	if url == "" {
		fmt.Fprintln(os.Stderr, "DATABASE_URL is required; do not print its value")
		os.Exit(2)
	}
	ctx := context.Background()
	db, err := pgxpool.New(ctx, url)
	if err != nil {
		panic(err)
	}
	defer db.Close()
	tx, err := db.Begin(ctx)
	if err != nil {
		panic(err)
	}
	defer tx.Rollback(ctx)
	var count int64
	if err = tx.QueryRow(ctx, `SELECT COUNT(*) FROM portal_users`).Scan(&count); err != nil {
		panic(err)
	}
	var id int64
	if err = tx.QueryRow(ctx, `INSERT INTO portal_users(username,display_name,password_hash,is_admin) VALUES($1,$2,$3,$4) RETURNING id`, username, display, hash, count == 0).Scan(&id); err != nil {
		panic(err)
	}
	if count == 0 {
		if _, err = tx.Exec(ctx, `UPDATE folders SET owner_id=$1 WHERE owner_id IS NULL`, id); err != nil {
			panic(err)
		}
		if _, err = tx.Exec(ctx, `UPDATE note_templates SET owner_id=$1 WHERE owner_id IS NULL`, id); err != nil {
			panic(err)
		}
	}
	if err = tx.Commit(ctx); err != nil {
		panic(err)
	}
	fmt.Printf("User %s created (id=%d, admin=%t).\n", username, id, count == 0)
}
