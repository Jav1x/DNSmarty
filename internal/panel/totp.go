package panel

import (
	"encoding/base64"
	"errors"
	"net/http"
	"time"

	"github.com/skip2/go-qrcode"

	"dnsmarty/internal/store"
)

func (s *Server) totpSetup(w http.ResponseWriter, r *http.Request) {
	sess := sessionFrom(r.Context())
	setup, err := s.store.BeginTOTP(r.Context(), sess.UserID)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	png, err := qrcode.Encode(setup.URL, qrcode.Medium, 256)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{
		"secret": setup.Secret,
		"url":    setup.URL,
		"qr_png": base64.StdEncoding.EncodeToString(png),
	})
}

func (s *Server) totpEnable(w http.ResponseWriter, r *http.Request) {
	sess := sessionFrom(r.Context())
	var body struct {
		Code string `json:"code"`
	}
	if !readJSON(w, r, &body) {
		return
	}
	codes, err := s.store.EnableTOTP(r.Context(), sess.Username, sess.UserID, body.Code, time.Now())
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"codes": codes})
}

func (s *Server) totpDisable(w http.ResponseWriter, r *http.Request) {
	sess := sessionFrom(r.Context())
	var body struct {
		Password string `json:"password"`
	}
	if !readJSON(w, r, &body) {
		return
	}
	err := s.store.DisableTOTP(r.Context(), sess.Username, sess.UserID, body.Password)
	if errors.Is(err, store.ErrWrongPassword) {
		writeErr(w, http.StatusBadRequest, "wrong_password", "The current password is wrong.")
		return
	}
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *Server) totpCodes(w http.ResponseWriter, r *http.Request) {
	sess := sessionFrom(r.Context())
	enabled, codes, err := s.store.RecoveryCodes(r.Context(), sess.UserID)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"enabled": enabled, "codes": codes})
}

func (s *Server) totpCodeUse(w http.ResponseWriter, r *http.Request) {
	sess := sessionFrom(r.Context())
	if err := s.store.UseRecoveryCode(r.Context(), sess.UserID, r.PathValue("id")); err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *Server) loginTOTP(w http.ResponseWriter, r *http.Request) {
	if !sameOrigin(r) {
		writeErr(w, http.StatusForbidden, "csrf", "Request rejected: reload the page.")
		return
	}
	var body struct {
		Ticket string `json:"ticket"`
		Code   string `json:"code"`
	}
	if !readJSON(w, r, &body) {
		return
	}
	ip := s.clientIP(r).String()
	if wait := s.limiter.blocked(ip, "totp"); wait > 0 {
		w.Header().Set("Retry-After", retryAfter(wait))
		writeErr(w, http.StatusTooManyRequests, "rate_limited", "Too many attempts. Try again later.")
		return
	}
	userID, username, err := s.store.RedeemLoginTicket(r.Context(), body.Ticket, body.Code, time.Now())
	if err != nil {
		s.limiter.fail(ip, "totp")
		s.audit(r, "totp", "login.fail", map[string]string{"ip": ip, "reason": "totp"})
		writeErr(w, http.StatusUnauthorized, "bad_code", "Wrong code.")
		return
	}
	s.limiter.success(ip)
	cookie, csrf, err := s.store.CreateSession(r.Context(), userID, ip, r.UserAgent())
	if err != nil {
		s.log.Error("session", "err", err)
		writeErr(w, http.StatusInternalServerError, "internal", "Session was not created.")
		return
	}
	s.audit(r, username, "login", map[string]string{"ip": ip})
	s.setCookie(w, cookie, int(store.SessionTTL.Seconds()))
	writeJSON(w, http.StatusOK, map[string]string{"user": username, "csrf": csrf})
}
