package store

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"
)

// OAuthProvider is one sign-in provider as the settings page sees it.
// The client secret is never included.
type OAuthProvider struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	ClientID  string `json:"client_id"`
	Enabled   bool   `json:"enabled"`
	HasSecret bool   `json:"has_secret"`
}

// OAuthLink is a provider row on the account page.
type OAuthLink struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	DisplayName string `json:"display_name,omitempty"`
	Linked      bool   `json:"linked"`
	Ready       bool   `json:"ready"`
}

type oauthKind struct {
	ID   string
	Name string
}

var oauthKinds = []oauthKind{
	{ID: "google", Name: "Google"},
	{ID: "github", Name: "GitHub"},
	{ID: "yandex", Name: "Yandex"},
}

func oauthKindByID(id string) (oauthKind, bool) {
	for _, k := range oauthKinds {
		if k.ID == id {
			return k, true
		}
	}
	return oauthKind{}, false
}

// OAuthProviders lists the three built-in providers. publicOnly keeps those
// that are enabled and have both a client id and a stored secret.
func (s *Store) OAuthProviders(ctx context.Context, publicOnly bool) ([]OAuthProvider, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT provider, client_id, enabled, secret_ciphertext IS NOT NULL
		FROM oauth_provider
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	got := map[string]OAuthProvider{}
	for rows.Next() {
		var p OAuthProvider
		if err := rows.Scan(&p.ID, &p.ClientID, &p.Enabled, &p.HasSecret); err != nil {
			return nil, err
		}
		got[p.ID] = p
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	out := []OAuthProvider{}
	for _, k := range oauthKinds {
		p := got[k.ID]
		p.ID = k.ID
		p.Name = k.Name
		if publicOnly && (!p.Enabled || p.ClientID == "" || !p.HasSecret) {
			continue
		}
		if publicOnly {
			p.ClientID = ""
		}
		out = append(out, p)
	}
	return out, nil
}

// SaveOAuth stores a client id and, when secret is non-empty, a new sealed secret.
// A blank secret keeps the previous one. Enabling requires both halves.
func (s *Store) SaveOAuth(ctx context.Context, actor, id, clientID, secret string, enabled bool) error {
	kind, ok := oauthKindByID(id)
	if !ok {
		return fmt.Errorf("%w: provider", ErrInvalid)
	}
	clientID = strings.TrimSpace(clientID)
	secret = strings.TrimSpace(secret)
	if len(clientID) > 256 || strings.ContainsAny(clientID, " \t\r\n") {
		return fmt.Errorf("%w: client id", ErrInvalid)
	}
	if len(secret) > 512 {
		return fmt.Errorf("%w: secret", ErrInvalid)
	}
	var nonce, ct []byte
	if secret != "" {
		var err error
		nonce, ct, err = s.seal([]byte(secret))
		if err != nil {
			return err
		}
	}
	return s.tx(ctx, func(tx pgx.Tx) error {
		var oldNonce, oldCT []byte
		err := tx.QueryRow(ctx, `
			SELECT secret_nonce, secret_ciphertext FROM oauth_provider WHERE provider = $1
		`, kind.ID).Scan(&oldNonce, &oldCT)
		if errors.Is(err, pgx.ErrNoRows) {
			oldNonce, oldCT = nil, nil
		} else if err != nil {
			return err
		}
		if secret == "" {
			nonce, ct = oldNonce, oldCT
		}
		if enabled && (clientID == "" || len(ct) == 0) {
			return fmt.Errorf("%w: secret", ErrInvalid)
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO oauth_provider (provider, client_id, secret_nonce, secret_ciphertext, enabled)
			VALUES ($1, $2, $3, $4, $5)
			ON CONFLICT (provider) DO UPDATE
			SET client_id = EXCLUDED.client_id,
			    secret_nonce = EXCLUDED.secret_nonce,
			    secret_ciphertext = EXCLUDED.secret_ciphertext,
			    enabled = EXCLUDED.enabled
		`, kind.ID, clientID, nonce, ct, enabled); err != nil {
			return err
		}
		detail, _ := json.Marshal(map[string]any{"provider": kind.ID, "enabled": enabled})
		return auditTx(ctx, tx, actor, "oauth.save", detail)
	})
}

// OAuthClient returns the id and decrypted secret of an enabled, complete provider.
func (s *Store) OAuthClient(ctx context.Context, id string) (clientID, secret string, err error) {
	if _, ok := oauthKindByID(id); !ok {
		return "", "", fmt.Errorf("%w: provider", ErrInvalid)
	}
	var nonce, ct []byte
	err = s.pool.QueryRow(ctx, `
		SELECT client_id, secret_nonce, secret_ciphertext
		FROM oauth_provider
		WHERE provider = $1 AND enabled AND client_id <> '' AND secret_ciphertext IS NOT NULL
	`, id).Scan(&clientID, &nonce, &ct)
	if err != nil {
		return "", "", mapErr(err)
	}
	plain, err := s.open(nonce, ct)
	if err != nil {
		return "", "", err
	}
	return clientID, string(plain), nil
}

// OAuthLinks reports which providers the user has connected, and which can start a link.
func (s *Store) OAuthLinks(ctx context.Context, userID string) ([]OAuthLink, error) {
	cfgs, err := s.OAuthProviders(ctx, false)
	if err != nil {
		return nil, err
	}
	rows, err := s.pool.Query(ctx, `SELECT provider, display_name FROM user_identity WHERE user_id = $1`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	linked := map[string]string{}
	for rows.Next() {
		var id, name string
		if err := rows.Scan(&id, &name); err != nil {
			return nil, err
		}
		linked[id] = name
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	out := make([]OAuthLink, 0, len(cfgs))
	for _, c := range cfgs {
		name, ok := linked[c.ID]
		out = append(out, OAuthLink{
			ID: c.ID, Name: c.Name, DisplayName: name, Linked: ok,
			Ready: c.Enabled && c.ClientID != "" && c.HasSecret,
		})
	}
	return out, nil
}

// LinkOAuth attaches a provider account to the user. A second link replaces the previous one.
// The same provider account cannot belong to two users.
func (s *Store) LinkOAuth(ctx context.Context, actor, userID, provider, uid, displayName string) error {
	if _, ok := oauthKindByID(provider); !ok || uid == "" || len(uid) > 256 {
		return fmt.Errorf("%w: provider", ErrInvalid)
	}
	displayName = clipDisplayName(displayName)
	return s.tx(ctx, func(tx pgx.Tx) error {
		var owner string
		err := tx.QueryRow(ctx, `
			SELECT user_id::text FROM user_identity WHERE provider = $1 AND provider_uid = $2
		`, provider, uid).Scan(&owner)
		if err == nil && owner != userID {
			return ErrConflict
		}
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return err
		}
		if _, err := tx.Exec(ctx, `DELETE FROM user_identity WHERE user_id = $1 AND provider = $2`, userID, provider); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO user_identity (provider, provider_uid, user_id, display_name) VALUES ($1, $2, $3, $4)
		`, provider, uid, userID, displayName); err != nil {
			return err
		}
		detail, _ := json.Marshal(map[string]string{"provider": provider})
		return auditTx(ctx, tx, actor, "oauth.link", detail)
	})
}

// UnlinkOAuth forgets the user's link to one provider.
func (s *Store) UnlinkOAuth(ctx context.Context, actor, userID, provider string) error {
	if _, ok := oauthKindByID(provider); !ok {
		return fmt.Errorf("%w: provider", ErrInvalid)
	}
	return s.tx(ctx, func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, `DELETE FROM user_identity WHERE user_id = $1 AND provider = $2`, userID, provider)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			return ErrNotFound
		}
		detail, _ := json.Marshal(map[string]string{"provider": provider})
		return auditTx(ctx, tx, actor, "oauth.unlink", detail)
	})
}

// SetOAuthDisplayName stores the name the provider last sent for this identity.
// A blank name leaves the previous value.
func (s *Store) SetOAuthDisplayName(ctx context.Context, provider, uid, displayName string) error {
	displayName = clipDisplayName(displayName)
	if displayName == "" {
		return nil
	}
	tag, err := s.pool.Exec(ctx, `
		UPDATE user_identity SET display_name = $3 WHERE provider = $1 AND provider_uid = $2
	`, provider, uid, displayName)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

func clipDisplayName(s string) string {
	s = strings.Join(strings.Fields(strings.TrimSpace(s)), " ")
	r := []rune(s)
	if len(r) > 80 {
		s = string(r[:80])
	}
	return s
}

// UserByOAuth finds the panel user a provider account is linked to.
func (s *Store) UserByOAuth(ctx context.Context, provider, uid string) (userID, username string, err error) {
	err = s.pool.QueryRow(ctx, `
		SELECT u.id::text, u.username
		FROM user_identity i
		JOIN admin_user u ON u.id = i.user_id
		WHERE i.provider = $1 AND i.provider_uid = $2
	`, provider, uid).Scan(&userID, &username)
	return userID, username, mapErr(err)
}

// OAuthPending is the sealed browser cookie that ties a callback to its start.
type OAuthPending struct {
	State    string `json:"s"`
	Provider string `json:"p"`
	Purpose  string `json:"u"`
	UserID   string `json:"uid,omitempty"`
	Username string `json:"name,omitempty"`
	Redirect string `json:"r"`
}

// SealOAuthPending encrypts a pending OAuth attempt into a cookie value.
func (s *Store) SealOAuthPending(p OAuthPending) (string, error) {
	raw, err := json.Marshal(p)
	if err != nil {
		return "", err
	}
	nonce, ct, err := s.seal(raw)
	if err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(append(nonce, ct...)), nil
}

// OpenOAuthPending decrypts a cookie written by SealOAuthPending.
func (s *Store) OpenOAuthPending(raw string) (OAuthPending, error) {
	buf, err := base64.RawURLEncoding.DecodeString(raw)
	if err != nil || len(buf) < 13 {
		return OAuthPending{}, fmt.Errorf("%w: state", ErrInvalid)
	}
	plain, err := s.open(buf[:12], buf[12:])
	if err != nil {
		return OAuthPending{}, fmt.Errorf("%w: state", ErrInvalid)
	}
	var p OAuthPending
	if err := json.Unmarshal(plain, &p); err != nil || p.State == "" || p.Provider == "" {
		return OAuthPending{}, fmt.Errorf("%w: state", ErrInvalid)
	}
	return p, nil
}
