package dns

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	mdns "github.com/miekg/dns"
)

func answerA(ip string) mdns.HandlerFunc {
	return func(w mdns.ResponseWriter, r *mdns.Msg) {
		m := new(mdns.Msg)
		m.SetReply(r)
		m.Answer = []mdns.RR{&mdns.A{
			Hdr: mdns.RR_Header{Name: r.Question[0].Name, Rrtype: mdns.TypeA, Class: mdns.ClassINET, Ttl: 60},
			A:   net.ParseIP(ip),
		}}
		_ = w.WriteMsg(m)
	}
}

func selfSigned(t *testing.T, name string) (tls.Certificate, *x509.CertPool) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tpl := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: name},
		DNSNames:     []string{name},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
	}
	der, err := x509.CreateCertificate(rand.Reader, tpl, tpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	leaf, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatal(err)
	}
	pool := x509.NewCertPool()
	pool.AddCert(leaf)
	return tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key}, pool
}

func startDoT(t *testing.T, cert tls.Certificate, answer string) string {
	t.Helper()
	ln, err := tls.Listen("tcp", "127.0.0.1:0", &tls.Config{Certificates: []tls.Certificate{cert}})
	if err != nil {
		t.Fatal(err)
	}
	srv := &mdns.Server{Listener: ln, Handler: answerA(answer)}
	go func() { _ = srv.ActivateAndServe() }()
	t.Cleanup(func() { _ = srv.Shutdown() })
	return ln.Addr().String()
}

func startUDP(t *testing.T, answer string) string {
	t.Helper()
	pc, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	srv := &mdns.Server{PacketConn: pc, Handler: answerA(answer)}
	go func() { _ = srv.ActivateAndServe() }()
	t.Cleanup(func() { _ = srv.Shutdown() })
	return pc.LocalAddr().String()
}

func aQuery() *mdns.Msg {
	q := new(mdns.Msg)
	q.SetQuestion("x.test.", mdns.TypeA)
	return q
}

func TestUpstreamProto(t *testing.T) {
	cases := map[string]string{
		"1.1.1.1:53":                "udp",
		"1.1.1.1:853":               "dot",
		"tls://dns.example.com:853": "dot",
		"https://dns.example.com/q": "doh",
	}
	for in, want := range cases {
		if got := UpstreamProto(in); got != want {
			t.Errorf("%q: got %s, want %s", in, got, want)
		}
	}
}

func TestDoTCertMismatchFallsThroughToNextUpstream(t *testing.T) {
	cert, pool := selfSigned(t, "dot.test")
	dot := "tls://" + startDoT(t, cert, "192.0.2.7")
	udp := startUDP(t, "192.0.2.9")
	e := NewEngine(nil)
	e.dotRoots = pool
	r, err := e.forward(aQuery(), []string{dot, udp})
	if err != nil {
		t.Fatal(err)
	}
	a, ok := r.Answer[0].(*mdns.A)
	if !ok || !a.A.Equal(net.ParseIP("192.0.2.9")) {
		t.Fatalf("answer = %v, want fallback 192.0.2.9", r.Answer)
	}
}

func TestDoHRejectsHTTPError(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte("garbage"))
	}))
	t.Cleanup(srv.Close)
	e := NewEngine(nil)
	e.doh = srv.Client()
	if r, err := e.forward(aQuery(), []string{srv.URL}); err == nil {
		t.Fatalf("HTTP 500 accepted, answer = %v", r)
	}
}

func TestDoHRejectsMalformedBody(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/dns-message")
		_, _ = w.Write([]byte("garbage"))
	}))
	t.Cleanup(srv.Close)
	e := NewEngine(nil)
	e.doh = srv.Client()
	if r, err := e.forward(aQuery(), []string{srv.URL}); err == nil {
		t.Fatalf("malformed body accepted, answer = %v", r)
	}
}
