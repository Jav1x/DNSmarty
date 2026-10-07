package store

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

var (
	ErrNotFound     = errors.New("not found")
	ErrUnauthorized = errors.New("unauthorized")
	ErrConflict     = errors.New("conflict")
	ErrInvalid      = errors.New("invalid")
)

type Store struct {
	pool   *pgxpool.Pool
	secret string
	master []byte
}

func Open(ctx context.Context, dsn, sessionSecret, masterKey string) (*Store, error) {
	if len(sessionSecret) < 16 {
		return nil, fmt.Errorf("SESSION_SECRET must be at least 16 characters")
	}
	if len(masterKey) < 16 {
		return nil, fmt.Errorf("PANEL_MASTER_KEY must be at least 16 characters")
	}
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		return nil, err
	}
	if cfg.ConnConfig.RuntimeParams == nil {
		cfg.ConnConfig.RuntimeParams = map[string]string{}
	}
	cfg.ConnConfig.RuntimeParams["TimeZone"] = "UTC"
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, err
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, err
	}
	sum := sha256.Sum256([]byte(masterKey))
	return &Store{pool: pool, secret: sessionSecret, master: sum[:]}, nil
}

func (s *Store) seal(plain []byte) (nonce, ciphertext []byte, err error) {
	block, err := aes.NewCipher(s.master)
	if err != nil {
		return nil, nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, nil, err
	}
	nonce = make([]byte, gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return nil, nil, err
	}
	return nonce, gcm.Seal(nil, nonce, plain, nil), nil
}

func (s *Store) open(nonce, ciphertext []byte) ([]byte, error) {
	block, err := aes.NewCipher(s.master)
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	return gcm.Open(nil, nonce, ciphertext, nil)
}

func (s *Store) Close() { s.pool.Close() }

func (s *Store) Ping(ctx context.Context) error { return s.pool.Ping(ctx) }

func HashToken(token string) []byte {
	sum := sha256.Sum256([]byte(token))
	return sum[:]
}

func (s *Store) sessionHash(token string) []byte {
	mac := hmac.New(sha256.New, []byte(s.secret))
	_, _ = mac.Write([]byte(token))
	return mac.Sum(nil)
}

func (s *Store) tx(ctx context.Context, fn func(pgx.Tx) error) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := fn(tx); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func randomHex(n int) (string, error) {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

func mapErr(err error) error {
	var pg *pgconn.PgError
	if errors.As(err, &pg) && pg.Code == "23505" {
		return fmt.Errorf("%w", ErrConflict)
	}
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	return err
}

func auditTx(ctx context.Context, tx pgx.Tx, actor, action string, detail []byte) error {
	if len(detail) == 0 {
		detail = []byte(`{}`)
	}
	_, err := tx.Exec(ctx, `INSERT INTO audit_log (actor, action, detail) VALUES ($1, $2, $3::jsonb)`, actor, action, detail)
	return err
}

func inetOrNil(s string) any {
	if s == "" {
		return nil
	}
	return s
}
