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

const (
	bcryptCost = 12
	// SessionTTL is the absolute session lifetime; SessionIdle ends a session nobody uses.
	SessionTTL  = 12 * time.Hour
	SessionIdle = 2 * time.Hour
	// MinPassword and MaxPassword bound a new password in bytes; bcrypt ignores everything past 72.
	MinPassword = 12
	MaxPassword = 72
)

var (
	ErrWrongPassword = errors.New("wrong password")
	ErrWeakPassword  = errors.New("weak password")
)

// dummyHash is compared against when the user does not exist, so a wrong username
// takes as long as a wrong password and the username cannot be probed by timing.
var dummyHash, _ = bcrypt.GenerateFromPassword([]byte("dnsmarty-no-such-user"), bcryptCost)

type Session struct {
	ID       string
	UserID   string
	Username string
	CSRF     string
	Cookie   string
}

// SessionInfo is one row of the sessions list.
type SessionInfo struct {
	ID        string    `json:"id"`
	CreatedAt time.Time `json:"created_at"`
	LastSeen  time.Time `json:"last_seen_at"`
	ExpiresAt time.Time `json:"expires_at"`
	IP        string    `json:"ip"`
	UserAgent string    `json:"user_agent"`
	Current   bool      `json:"current"`
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
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcryptCost)
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
		_ = bcrypt.CompareHashAndPassword(dummyHash, []byte(password))
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	if bcrypt.CompareHashAndPassword([]byte(hash), []byte(password)) == nil {
		return id, true, nil
	}
	return "", false, nil
}

// CreateSession stores a new session and returns the cookie value and the CSRF token.
// Only an HMAC of the cookie is stored.
func (s *Store) CreateSession(ctx context.Context, userID, ip, userAgent string) (cookie, csrf string, err error) {
	cookie, err = randomHex(32)
	if err != nil {
		return "", "", err
	}
	csrf, err = randomHex(16)
	if err != nil {
		return "", "", err
	}
	_, err = s.pool.Exec(ctx, `
		INSERT INTO session (user_id, token_sha256, csrf, expires_at, ip, user_agent)
		VALUES ($1, $2, $3, $4, $5::inet, $6)
	`, userID, s.sessionHash(cookie), csrf, time.Now().UTC().Add(SessionTTL), inetOrNil(ip), clip(userAgent, 300))
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

// LookupSession finds a live session: not past its absolute expiry and used within SessionIdle.
// last_seen_at is refreshed at most once a minute to keep reads cheap.
func (s *Store) LookupSession(ctx context.Context, cookie string) (Session, error) {
	var sess Session
	var seen time.Time
	err := s.pool.QueryRow(ctx, `
		SELECT s.id::text, u.id::text, u.username, s.csrf, s.last_seen_at
		FROM session s
		JOIN admin_user u ON u.id = s.user_id
		WHERE s.token_sha256 = $1 AND s.expires_at > now() AND s.last_seen_at > now() - make_interval(secs => $2)
	`, s.sessionHash(cookie), SessionIdle.Seconds()).Scan(&sess.ID, &sess.UserID, &sess.Username, &sess.CSRF, &seen)
	if err != nil {
		return Session{}, mapErr(err)
	}
	if time.Since(seen) > time.Minute {
		if _, err := s.pool.Exec(ctx, `UPDATE session SET last_seen_at = now() WHERE id = $1`, sess.ID); err != nil {
			return Session{}, err
		}
	}
	sess.Cookie = cookie
	return sess, nil
}

func (s *Store) DeleteSession(ctx context.Context, cookie string) error {
	_, err := s.pool.Exec(ctx, `DELETE FROM session WHERE token_sha256 = $1`, s.sessionHash(cookie))
	return err
}

func (s *Store) ListSessions(ctx context.Context, userID, currentID string) ([]SessionInfo, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT id::text, created_at, last_seen_at, expires_at, coalesce(host(ip), ''), user_agent
		FROM session
		WHERE user_id = $1 AND expires_at > now() AND last_seen_at > now() - make_interval(secs => $2)
		ORDER BY last_seen_at DESC
	`, userID, SessionIdle.Seconds())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []SessionInfo{}
	for rows.Next() {
		var si SessionInfo
		if err := rows.Scan(&si.ID, &si.CreatedAt, &si.LastSeen, &si.ExpiresAt, &si.IP, &si.UserAgent); err != nil {
			return nil, err
		}
		si.Current = si.ID == currentID
		out = append(out, si)
	}
	return out, rows.Err()
}

// DeleteSessionByID revokes one session of this user.
func (s *Store) DeleteSessionByID(ctx context.Context, actor, userID, id string) error {
	if !uuidRe.MatchString(id) {
		return ErrNotFound
	}
	return s.tx(ctx, func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, `DELETE FROM session WHERE id = $1 AND user_id = $2`, id, userID)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			return ErrNotFound
		}
		raw, _ := json.Marshal(map[string]string{"id": id})
		return auditTx(ctx, tx, actor, "session.revoke", raw)
	})
}

// DeleteOtherSessions revokes every session of this user except keepID.
func (s *Store) DeleteOtherSessions(ctx context.Context, actor, userID, keepID string) (int64, error) {
	var n int64
	err := s.tx(ctx, func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, `DELETE FROM session WHERE user_id = $1 AND id <> $2`, userID, keepID)
		if err != nil {
			return err
		}
		n = tag.RowsAffected()
		raw, _ := json.Marshal(map[string]int64{"revoked": n})
		return auditTx(ctx, tx, actor, "session.revoke_others", raw)
	})
	return n, err
}

// ChangePassword checks the current password, stores the new hash and ends every other
// session of the user, so a stolen cookie stops working with the old password.
func (s *Store) ChangePassword(ctx context.Context, sess Session, current, next string) error {
	if len(next) < MinPassword || len(next) > MaxPassword {
		return ErrWeakPassword
	}
	if current == next {
		return ErrWeakPassword
	}
	var hash string
	if err := s.pool.QueryRow(ctx, `SELECT password_hash FROM admin_user WHERE id = $1`, sess.UserID).Scan(&hash); err != nil {
		return mapErr(err)
	}
	if bcrypt.CompareHashAndPassword([]byte(hash), []byte(current)) != nil {
		return ErrWrongPassword
	}
	newHash, err := bcrypt.GenerateFromPassword([]byte(next), bcryptCost)
	if err != nil {
		return err
	}
	return s.tx(ctx, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `UPDATE admin_user SET password_hash = $2, password_changed_at = now() WHERE id = $1`, sess.UserID, string(newHash)); err != nil {
			return err
		}
		tag, err := tx.Exec(ctx, `DELETE FROM session WHERE user_id = $1 AND id <> $2`, sess.UserID, sess.ID)
		if err != nil {
			return err
		}
		raw, _ := json.Marshal(map[string]int64{"revoked": tag.RowsAffected()})
		return auditTx(ctx, tx, sess.Username, "password.change", raw)
	})
}
