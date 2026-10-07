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

func ParseKey(s string) ([]byte, error) {
	s = strings.TrimSpace(s)
	b, err := hex.DecodeString(s)
	if err != nil || len(b) != 32 {
		return nil, errors.New("ключ должен быть 64 hex-символа")
	}
	return b, nil
}

func EncodeKey(raw []byte) string {
	return hex.EncodeToString(raw)
}

func privateKey(nodeKey []byte) (*ecdsa.PrivateKey, error) {
	// Go 1.26 ignores io.Reader in ecdsa.GenerateKey, so the scalar comes from HKDF directly.
	stream := hkdf.New(sha256.New, nodeKey, []byte("dnsmarty"), []byte("agent-tls-v1"))
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
		x, y := curve.ScalarBaseMult(scalar)
		return &ecdsa.PrivateKey{
			PublicKey: ecdsa.PublicKey{Curve: curve, X: x, Y: y},
			D:         d,
		}, nil
	}
}

func Certificate(nodeKey []byte) (tls.Certificate, error) {
	priv, err := privateKey(nodeKey)
	if err != nil {
		return tls.Certificate{}, err
	}
	tmpl := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "dnsmarty-agent"},
		NotBefore:             time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
		NotAfter:              time.Date(2036, 1, 1, 0, 0, 0, 0, time.UTC),
		KeyUsage:              x509.KeyUsageDigitalSignature,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &priv.PublicKey, priv)
	if err != nil {
		return tls.Certificate{}, err
	}
	return tls.Certificate{Certificate: [][]byte{der}, PrivateKey: priv}, nil
}

func ServerTLS(nodeKey []byte) (*tls.Config, error) {
	cert, err := Certificate(nodeKey)
	if err != nil {
		return nil, err
	}
	return &tls.Config{
		MinVersion:   tls.VersionTLS12,
		Certificates: []tls.Certificate{cert},
	}, nil
}

func ClientTLS(nodeKey []byte) (*tls.Config, error) {
	priv, err := privateKey(nodeKey)
	if err != nil {
		return nil, err
	}
	want := &priv.PublicKey
	return &tls.Config{
		MinVersion:         tls.VersionTLS12,
		InsecureSkipVerify: true,
		VerifyPeerCertificate: func(raw [][]byte, _ [][]*x509.Certificate) error {
			if len(raw) == 0 {
				return errors.New("нет сертификата")
			}
			cert, err := x509.ParseCertificate(raw[0])
			if err != nil {
				return err
			}
			got, ok := cert.PublicKey.(*ecdsa.PublicKey)
			if !ok || got.X.Cmp(want.X) != 0 || got.Y.Cmp(want.Y) != 0 {
				return errors.New("ключ не совпал")
			}
			return nil
		},
	}, nil
}

func NewKey() ([]byte, error) {
	b := make([]byte, 32)
	if _, err := io.ReadFull(rand.Reader, b); err != nil {
		return nil, fmt.Errorf("key: %w", err)
	}
	return b, nil
}
