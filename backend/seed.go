package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"

	"github.com/jackc/pgx/v5/pgxpool"
)

// Explicit operator action only. Uses normal single-use code verification, not an auth bypass.
func seedPreview(ctx context.Context, db *pgxpool.Pool, out io.Writer) error {
	email, ok := normalizeEmail(os.Getenv("TIDAL_SEED_EMAIL"))
	if !ok {
		return errors.New("TIDAL_SEED_EMAIL must be a valid preview address")
	}
	password, err := randomString(24)
	if err != nil {
		return err
	}
	code, err := newCode()
	if err != nil {
		return err
	}
	passwordHash, err := hashSecret(password)
	if err != nil {
		return err
	}
	codeHash, err := hashSecret(code)
	if err != nil {
		return err
	}
	challenge, err := randomString(24)
	if err != nil {
		return err
	}
	tx, err := db.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	var userID int64
	if err := tx.QueryRow(ctx, `INSERT INTO users (email, name, password_hash, tier, email_verified_at)
		VALUES ($1, 'Preview owner', $2, 'premier', now()) RETURNING id`, email, passwordHash).Scan(&userID); err != nil {
		return errors.New("preview account could not be created; existing accounts are never overwritten")
	}
	if _, err := tx.Exec(ctx, `INSERT INTO login_challenges (id, user_id, code_hash, expires_at)
		VALUES ($1, $2, $3, now() + interval '30 minutes')`, challenge, userID, codeHash); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return err
	}
	_, err = fmt.Fprintf(out, "Email: %s\nPassword: %s\nOne-time preview code: %s\nCode expires in 30 minutes. Normal session expires in 12 hours.\n", email, password, code)
	return err
}

// ensureAdmin creates or repairs the platform admin from TIDAL_ADMIN_EMAIL and TIDAL_ADMIN_PASSWORD.
// It is idempotent and never overwrites a stored password unless TIDAL_ADMIN_PASSWORD_RESET=1, so a
// stale environment value cannot undo a password the admin changed. Secrets are never logged.
func ensureAdmin(ctx context.Context, db *pgxpool.Pool, cfg config) error {
	email, ok := normalizeEmail(cfg.AdminEmail)
	if !ok || cfg.AdminPassword == "" {
		// No password admin configured: the verified Google sign-in path keeps bootstrapping the admin.
		return nil
	}
	if len(cfg.AdminPassword) < 12 || len(cfg.AdminPassword) > 128 {
		return errors.New("TIDAL_ADMIN_PASSWORD must be 12-128 characters")
	}
	hash, err := hashSecret(cfg.AdminPassword)
	if err != nil {
		return err
	}
	// One statement keeps replicas from racing each other into a duplicate row.
	_, err = db.Exec(ctx, `
		INSERT INTO users (email, name, password_hash, tier, is_platform_admin, email_verified_at)
		VALUES ($1, 'Platform admin', $2, 'premier', true, now())
		ON CONFLICT (email) DO UPDATE SET
			is_platform_admin = true,
			tier = 'premier',
			email_verified_at = COALESCE(users.email_verified_at, now()),
			password_hash = CASE WHEN $3::boolean THEN EXCLUDED.password_hash ELSE users.password_hash END`,
		email, hash, cfg.AdminPasswordReset)
	return err
}
