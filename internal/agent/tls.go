package agent

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"math/big"
	"strings"
	"time"

	"golang.org/x/crypto/hkdf"
)

// Both sides derive two key pairs from the shared node key. The agent proves it holds
// the key with agentInfo, the panel with panelInfo, so neither side can impersonate the other
// to a third party that only saw one certificate.
const (
	agentInfo = "agent-tls-v1"
	panelInfo = "panel-tls-v1"
)

func ParseKey(s string) ([]byte, error) {
	s = strings.TrimSpace(s)
	b, err := hex.DecodeString(s)
	if err != nil || len(b) != 32 {
		return nil, errors.New("key must be 64 hex characters")
	}
	return b, nil
}

func EncodeKey(raw []byte) string {
	return hex.EncodeToString(raw)
}

func privateKey(nodeKey []byte, info string) (*ecdsa.PrivateKey, error) {
	// Go 1.26 ignores io.Reader in ecdsa.GenerateKey, so the scalar comes from HKDF directly.
	stream := hkdf.New(sha256.New, nodeKey, []byte("dnsmarty"), []byte(info))
	curve := elliptic.P256()
	n := curve.Params().N
	buf := make([]byte, 32)
	for {
		if _, err := io.ReadFull(stream, buf); err != nil {
			return nil, err
		}
		d := new(big.Int).SetBytes(buf)
		if d.Sign() == 0 || d.Cmp(n) >= 0 {
			continue
		}
		scalar := d.FillBytes(make([]byte, 32))
		// ParseRawPrivateKey validates the scalar and derives the public key itself;
		// the raw X/Y/D fields are deprecated in Go 1.26.
		priv, err := ecdsa.ParseRawPrivateKey(curve, scalar)
		if err != nil {
			continue
		}
		return priv, nil
	}
}

func certificate(priv *ecdsa.PrivateKey, cn string, usage x509.ExtKeyUsage) (tls.Certificate, error) {
	tmpl := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: cn},
		NotBefore:             time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
		NotAfter:              time.Date(2036, 1, 1, 0, 0, 0, 0, time.UTC),
		KeyUsage:              x509.KeyUsageDigitalSignature,
		ExtKeyUsage:           []x509.ExtKeyUsage{usage},
		BasicConstraintsValid: true,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &priv.PublicKey, priv)
	if err != nil {
		return tls.Certificate{}, err
	}
	return tls.Certificate{Certificate: [][]byte{der}, PrivateKey: priv}, nil
}

// pin accepts the peer only if its leaf certificate carries exactly this public key.
// VerifyConnection runs on every handshake, resumed ones included.
func pin(want *ecdsa.PublicKey) func(tls.ConnectionState) error {
	return func(cs tls.ConnectionState) error {
		if len(cs.PeerCertificates) == 0 {
			return errors.New("no certificate")
		}
		got, ok := cs.PeerCertificates[0].PublicKey.(*ecdsa.PublicKey)
		if !ok || !got.Equal(want) {
			return errors.New("The key does not match.")
		}
		return nil
	}
}

func Certificate(nodeKey []byte) (tls.Certificate, error) {
	priv, err := privateKey(nodeKey, agentInfo)
	if err != nil {
		return tls.Certificate{}, err
	}
	return certificate(priv, "dnsmarty-agent", x509.ExtKeyUsageServerAuth)
}

// ServerTLS is the agent side: it presents the agent certificate and requires the panel one.
func ServerTLS(nodeKey []byte) (*tls.Config, error) {
	cert, err := Certificate(nodeKey)
	if err != nil {
		return nil, err
	}
	panel, err := privateKey(nodeKey, panelInfo)
	if err != nil {
		return nil, err
	}
	return &tls.Config{
		MinVersion:             tls.VersionTLS13,
		Certificates:           []tls.Certificate{cert},
		ClientAuth:             tls.RequireAnyClientCert,
		VerifyConnection:       pin(&panel.PublicKey),
		SessionTicketsDisabled: true,
	}, nil
}

// ClientTLS is the panel side: it presents the panel certificate and pins the agent one.
func ClientTLS(nodeKey []byte) (*tls.Config, error) {
	agent, err := privateKey(nodeKey, agentInfo)
	if err != nil {
		return nil, err
	}
	panel, err := privateKey(nodeKey, panelInfo)
	if err != nil {
		return nil, err
	}
	cert, err := certificate(panel, "dnsmarty-panel", x509.ExtKeyUsageClientAuth)
	if err != nil {
		return nil, err
	}
	return &tls.Config{
		MinVersion:   tls.VersionTLS13,
		Certificates: []tls.Certificate{cert},
		// Self-signed certificates have no chain to verify; pin replaces chain validation.
		InsecureSkipVerify: true,
		VerifyConnection:   pin(&agent.PublicKey),
	}, nil
}

func NewKey() ([]byte, error) {
	b := make([]byte, 32)
	if _, err := io.ReadFull(rand.Reader, b); err != nil {
		return nil, fmt.Errorf("key: %w", err)
	}
	return b, nil
}
