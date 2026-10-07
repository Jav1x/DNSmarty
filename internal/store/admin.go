package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"golang.org/x/crypto/bcrypt"
)

type Session struct {
	UserID   string
	Username string
	CSRF     string
	Cookie   string
}

func (s *Store) EnsureAdmin(ctx context.Context, username, password string) error {
	var n int
	if err := s.pool.QueryRow(ctx, `SELECT count(*) FROM admin_user`).Scan(&n); err != nil {
		return err
	}
	if n > 0 {
		return nil
	}
	if username == "" || password == "" {
		return fmt.Errorf("PANEL_ADMIN_USER and PANEL_ADMIN_PASSWORD are required until an admin exists")
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(password), 12)
	if err != nil {
		return err
	}
	_, err = s.pool.Exec(ctx, `INSERT INTO admin_user (username, password_hash) VALUES ($1, $2)`, username, string(hash))
	return err
}

func (s *Store) Authenticate(ctx context.Context, username, password string) (string, bool, error) {
	var id, hash string
	err := s.pool.QueryRow(ctx, `SELECT id::text, password_hash FROM admin_user WHERE username = $1`, username).Scan(&id, &hash)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	if bcrypt.CompareHashAndPassword([]byte(hash), []byte(password)) != nil {
		return "", false, nil
	}
	return id, true, nil
}

func (s *Store) CreateSession(ctx context.Context, userID string) (cookie, csrf string, err error) {
	cookie, err = randomHex(32)
	if err != nil {
		return "", "", err
	}
	csrf, err = randomHex(16)
	if err != nil {
		return "", "", err
	}
	_, err = s.pool.Exec(ctx, `
		INSERT INTO session (user_id, token_sha256, csrf, expires_at)
		VALUES ($1, $2, $3, $4)
	`, userID, s.sessionHash(cookie), csrf, time.Now().UTC().Add(12*time.Hour))
	if err != nil {
		return "", "", err
	}
	return cookie, csrf, nil
}

func (s *Store) RecordAudit(ctx context.Context, actor, action string, detail any) error {
	raw, err := json.Marshal(detail)
	if err != nil || detail == nil {
		raw = []byte(`{}`)
	}
	_, err = s.pool.Exec(ctx, `INSERT INTO audit_log (actor, action, detail) VALUES ($1, $2, $3::jsonb)`, actor, action, raw)
	return err
}

func (s *Store) LookupSession(ctx context.Context, cookie string) (Session, error) {
	var sess Session
	err := s.pool.QueryRow(ctx, `
		SELECT u.id::text, u.username, s.csrf
		FROM session s
		JOIN admin_user u ON u.id = s.user_id
		WHERE s.token_sha256 = $1 AND s.expires_at > now()
	`, s.sessionHash(cookie)).Scan(&sess.UserID, &sess.Username, &sess.CSRF)
	if err != nil {
		return Session{}, mapErr(err)
	}
	sess.Cookie = cookie
	return sess, nil
}

func (s *Store) DeleteSession(ctx context.Context, cookie string) error {
	_, err := s.pool.Exec(ctx, `DELETE FROM session WHERE token_sha256 = $1`, s.sessionHash(cookie))
	return err
}

func (s *Store) SetFlash(ctx context.Context, cookie, msg string) error {
	tag, err := s.pool.Exec(ctx, `UPDATE session SET flash = $2 WHERE token_sha256 = $1`, s.sessionHash(cookie), msg)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *Store) TakeFlash(ctx context.Context, cookie string) (string, error) {
	var msg string
	err := s.pool.QueryRow(ctx, `
		WITH old AS (
			SELECT id, flash FROM session WHERE token_sha256 = $1
		)
		UPDATE session s SET flash = ''
		FROM old
		WHERE s.id = old.id
		RETURNING old.flash
	`, s.sessionHash(cookie)).Scan(&msg)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", nil
	}
	return msg, err
}
