package store

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/pquerna/otp"
	"github.com/pquerna/otp/totp"
	"golang.org/x/crypto/bcrypt"
)

const (
	totpIssuer       = "DNSmarty"
	totpPeriod       = 30
	loginTicketTTL   = 5 * time.Minute
	maxTicketAttempt = 5
	recoveryCodesN   = 8
)

// RecoveryCode is one backup code the account page can show. Code is empty once it is used.
type RecoveryCode struct {
	ID   string `json:"id"`
	Code string `json:"code,omitempty"`
	Used bool   `json:"used"`
}

// TOTPSetup is the secret an authenticator app must store before two-factor is turned on.
type TOTPSetup struct {
	Secret string
	URL    string
}

// BeginTOTP stores a new encrypted secret and leaves two-factor off until EnableTOTP.
// A second call replaces a secret that was never confirmed. It refuses once two-factor is on.
func (s *Store) BeginTOTP(ctx context.Context, userID string) (TOTPSetup, error) {
	var username string
	var enabled bool
	err := s.pool.QueryRow(ctx, `SELECT username, totp_enabled FROM admin_user WHERE id = $1`, userID).Scan(&username, &enabled)
	if err != nil {
		return TOTPSetup{}, mapErr(err)
	}
	if enabled {
		return TOTPSetup{}, fmt.Errorf("%w: 2fa", ErrInvalid)
	}
	key, err := totp.Generate(totp.GenerateOpts{
		Issuer:      totpIssuer,
		AccountName: username,
		Period:      totpPeriod,
		SecretSize:  20,
		Digits:      otp.DigitsSix,
		Algorithm:   otp.AlgorithmSHA1,
	})
	if err != nil {
		return TOTPSetup{}, err
	}
	nonce, ct, err := s.seal([]byte(key.Secret()))
	if err != nil {
		return TOTPSetup{}, err
	}
	err = s.tx(ctx, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `
			UPDATE admin_user
			SET totp_nonce = $2, totp_ciphertext = $3, totp_enabled = false, totp_last_step = NULL
			WHERE id = $1
		`, userID, nonce, ct); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, `DELETE FROM recovery_code WHERE user_id = $1`, userID)
		return err
	})
	if err != nil {
		return TOTPSetup{}, err
	}
	return TOTPSetup{Secret: key.Secret(), URL: key.URL()}, nil
}

// EnableTOTP checks the authenticator code, turns two-factor on, and returns eight backup codes.
// The code just used cannot be used again to sign in.
func (s *Store) EnableTOTP(ctx context.Context, actor, userID, code string, now time.Time) ([]string, error) {
	secret, last, enabled, err := s.totpSecret(ctx, userID)
	if err != nil {
		return nil, err
	}
	if enabled || secret == "" {
		return nil, fmt.Errorf("%w: 2fa", ErrInvalid)
	}
	step, ok := totpStep(secret, normalizeCode(code), now)
	if !ok || (last != nil && step <= *last) {
		return nil, fmt.Errorf("%w: code", ErrInvalid)
	}
	displays := make([]string, 0, recoveryCodesN)
	type sealed struct {
		display, norm string
		nonce, ct     []byte
	}
	rows := make([]sealed, 0, recoveryCodesN)
	seen := map[string]struct{}{}
	for len(rows) < recoveryCodesN {
		display, norm, err := newRecoveryCode()
		if err != nil {
			return nil, err
		}
		if _, dup := seen[norm]; dup {
			continue
		}
		seen[norm] = struct{}{}
		nonce, ct, err := s.seal([]byte(display))
		if err != nil {
			return nil, err
		}
		rows = append(rows, sealed{display: display, norm: norm, nonce: nonce, ct: ct})
		displays = append(displays, display)
	}
	err = s.tx(ctx, func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, `
			UPDATE admin_user
			SET totp_enabled = true, totp_last_step = $2
			WHERE id = $1 AND totp_enabled = false
		`, userID, step)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			return fmt.Errorf("%w: 2fa", ErrInvalid)
		}
		if _, err := tx.Exec(ctx, `DELETE FROM recovery_code WHERE user_id = $1`, userID); err != nil {
			return err
		}
		for i, row := range rows {
			if _, err := tx.Exec(ctx, `
				INSERT INTO recovery_code (user_id, code_hash, code_nonce, code_ciphertext, ordinal)
				VALUES ($1, $2, $3, $4, $5)
			`, userID, codeHash(row.norm), row.nonce, row.ct, i+1); err != nil {
				return err
			}
		}
		return auditTx(ctx, tx, actor, "totp.enable", []byte(`{}`))
	})
	if err != nil {
		return nil, err
	}
	return displays, nil
}

// DisableTOTP checks the password, then forgets the secret and every backup code.
func (s *Store) DisableTOTP(ctx context.Context, actor, userID, password string) error {
	var hash string
	if err := s.pool.QueryRow(ctx, `SELECT password_hash FROM admin_user WHERE id = $1`, userID).Scan(&hash); err != nil {
		return mapErr(err)
	}
	if bcrypt.CompareHashAndPassword([]byte(hash), []byte(password)) != nil {
		return ErrWrongPassword
	}
	return s.tx(ctx, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `
			UPDATE admin_user
			SET totp_enabled = false, totp_nonce = NULL, totp_ciphertext = NULL, totp_last_step = NULL
			WHERE id = $1
		`, userID); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `DELETE FROM recovery_code WHERE user_id = $1`, userID); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `DELETE FROM login_ticket WHERE user_id = $1`, userID); err != nil {
			return err
		}
		return auditTx(ctx, tx, actor, "totp.disable", []byte(`{}`))
	})
}

// TOTPEnabled reports whether sign-in must ask for a second code.
func (s *Store) TOTPEnabled(ctx context.Context, userID string) (bool, error) {
	var enabled bool
	err := s.pool.QueryRow(ctx, `SELECT totp_enabled FROM admin_user WHERE id = $1`, userID).Scan(&enabled)
	return enabled, mapErr(err)
}

// RecoveryCodes decrypts the account's backup codes. A used code comes back without its text.
func (s *Store) RecoveryCodes(ctx context.Context, userID string) (bool, []RecoveryCode, error) {
	enabled, err := s.TOTPEnabled(ctx, userID)
	if err != nil {
		return false, nil, err
	}
	rows, err := s.pool.Query(ctx, `
		SELECT id::text, code_nonce, code_ciphertext, used_at IS NOT NULL
		FROM recovery_code WHERE user_id = $1 ORDER BY ordinal
	`, userID)
	if err != nil {
		return false, nil, err
	}
	defer rows.Close()
	out := []RecoveryCode{}
	for rows.Next() {
		var rc RecoveryCode
		var nonce, ct []byte
		if err := rows.Scan(&rc.ID, &nonce, &ct, &rc.Used); err != nil {
			return false, nil, err
		}
		if !rc.Used {
			plain, err := s.open(nonce, ct)
			if err != nil {
				return false, nil, err
			}
			rc.Code = string(plain)
		}
		out = append(out, rc)
	}
	return enabled, out, rows.Err()
}

// UseRecoveryCode marks one backup code used from the account page.
func (s *Store) UseRecoveryCode(ctx context.Context, userID, id string) error {
	if !uuidRe.MatchString(id) {
		return ErrNotFound
	}
	tag, err := s.pool.Exec(ctx, `
		UPDATE recovery_code SET used_at = now()
		WHERE id = $1 AND user_id = $2 AND used_at IS NULL
	`, id, userID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// IssueLoginTicket remembers that the password was accepted. The ticket is not a session.
// A new ticket replaces any earlier one for the same user.
func (s *Store) IssueLoginTicket(ctx context.Context, userID string) (string, error) {
	ticket, err := randomHex(32)
	if err != nil {
		return "", err
	}
	err = s.tx(ctx, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `DELETE FROM login_ticket WHERE user_id = $1 OR expires_at < now()`, userID); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, `
			INSERT INTO login_ticket (token_sha256, user_id, expires_at)
			VALUES ($1, $2, $3)
		`, HashToken(ticket), userID, time.Now().UTC().Add(loginTicketTTL))
		return err
	})
	if err != nil {
		return "", err
	}
	return ticket, nil
}

// RedeemLoginTicket checks a one-time code or a backup code.
// A wrong code keeps the ticket until the fifth miss, then the ticket is dropped.
// The miss is committed: the caller's transaction wrapper must not roll it back.
func (s *Store) RedeemLoginTicket(ctx context.Context, ticket, code string, now time.Time) (userID, username string, err error) {
	sum := HashToken(ticket)
	denied := false
	err = s.tx(ctx, func(tx pgx.Tx) error {
		var attempts int
		var expires time.Time
		var nonce, ct []byte
		var last *int64
		var enabled bool
		qerr := tx.QueryRow(ctx, `
			SELECT t.user_id::text, u.username, t.attempts, t.expires_at,
			       u.totp_enabled, u.totp_nonce, u.totp_ciphertext, u.totp_last_step
			FROM login_ticket t
			JOIN admin_user u ON u.id = t.user_id
			WHERE t.token_sha256 = $1
			FOR UPDATE OF t
		`, sum).Scan(&userID, &username, &attempts, &expires, &enabled, &nonce, &ct, &last)
		if errors.Is(qerr, pgx.ErrNoRows) {
			userID, username = "", ""
			denied = true
			return nil
		}
		if qerr != nil {
			return qerr
		}
		if !enabled || !expires.After(now) || attempts >= maxTicketAttempt {
			if _, err := tx.Exec(ctx, `DELETE FROM login_ticket WHERE token_sha256 = $1`, sum); err != nil {
				return err
			}
			userID, username = "", ""
			denied = true
			return nil
		}
		secret, oerr := s.open(nonce, ct)
		if oerr != nil {
			return oerr
		}
		norm := normalizeCode(code)
		step, ok := totpStep(string(secret), norm, now)
		replay := ok && last != nil && step <= *last
		switch {
		case ok && !replay:
			if _, err := tx.Exec(ctx, `UPDATE admin_user SET totp_last_step = $2 WHERE id = $1`, userID, step); err != nil {
				return err
			}
			if _, err := tx.Exec(ctx, `DELETE FROM login_ticket WHERE token_sha256 = $1`, sum); err != nil {
				return err
			}
		case !ok:
			tag, err := tx.Exec(ctx, `
				UPDATE recovery_code SET used_at = $3
				WHERE user_id = $1 AND code_hash = $2 AND used_at IS NULL
			`, userID, codeHash(norm), now.UTC())
			if err != nil {
				return err
			}
			if tag.RowsAffected() == 1 {
				if _, err := tx.Exec(ctx, `DELETE FROM login_ticket WHERE token_sha256 = $1`, sum); err != nil {
					return err
				}
				return nil
			}
			fallthrough
		default:
			denied = true
			userID, username = "", ""
			if attempts+1 >= maxTicketAttempt {
				_, err := tx.Exec(ctx, `DELETE FROM login_ticket WHERE token_sha256 = $1`, sum)
				return err
			}
			_, err := tx.Exec(ctx, `UPDATE login_ticket SET attempts = attempts + 1 WHERE token_sha256 = $1`, sum)
			return err
		}
		return nil
	})
	if err != nil {
		return "", "", err
	}
	if denied {
		return "", "", ErrUnauthorized
	}
	return userID, username, nil
}

func (s *Store) totpSecret(ctx context.Context, userID string) (secret string, last *int64, enabled bool, err error) {
	var nonce, ct []byte
	err = s.pool.QueryRow(ctx, `
		SELECT totp_enabled, totp_nonce, totp_ciphertext, totp_last_step
		FROM admin_user WHERE id = $1
	`, userID).Scan(&enabled, &nonce, &ct, &last)
	if err != nil {
		return "", nil, false, mapErr(err)
	}
	if len(nonce) == 0 || len(ct) == 0 {
		return "", last, enabled, nil
	}
	plain, err := s.open(nonce, ct)
	if err != nil {
		return "", nil, false, err
	}
	return string(plain), last, enabled, nil
}

func totpStep(secret, code string, now time.Time) (int64, bool) {
	if len(code) != 6 {
		return 0, false
	}
	for _, ch := range code {
		if ch < '0' || ch > '9' {
			return 0, false
		}
	}
	opts := totp.ValidateOpts{Period: totpPeriod, Skew: 0, Digits: otp.DigitsSix, Algorithm: otp.AlgorithmSHA1}
	for _, delta := range []int64{0, -totpPeriod, totpPeriod} {
		at := now.Add(time.Duration(delta) * time.Second)
		ok, err := totp.ValidateCustom(code, secret, at, opts)
		if err == nil && ok {
			return at.Unix() / totpPeriod, true
		}
	}
	return 0, false
}

func newRecoveryCode() (display, norm string, err error) {
	const alphabet = "ABCDEFGHJKLMNPQRSTUVWXYZ23456789"
	raw := make([]byte, 10)
	if _, err = rand.Read(raw); err != nil {
		return "", "", err
	}
	buf := make([]byte, 10)
	for i := range buf {
		buf[i] = alphabet[int(raw[i])%len(alphabet)]
	}
	norm = string(buf)
	return norm[:5] + "-" + norm[5:], norm, nil
}

func normalizeCode(s string) string {
	s = strings.ToUpper(strings.TrimSpace(s))
	s = strings.ReplaceAll(s, " ", "")
	s = strings.ReplaceAll(s, "-", "")
	return s
}

func codeHash(norm string) []byte {
	sum := sha256.Sum256([]byte(norm))
	return sum[:]
}
