package proxy

import (
	"bufio"
	"context"
	"io"
	"net"
	"net/http"
	"net/netip"
	"strings"
	"testing"
	"time"

	"dnsmarty/internal/snapshot"
)

func startProxy(t *testing.T, snap snapshot.ProxySnap) (*Server, string, string) {
	t.Helper()
	s := New(nil, "", "")
	if err := s.SetSnapshot(&snap); err != nil {
		t.Fatal(err)
	}
	lnHTTP, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	lnTLS, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { _ = s.Serve(ctx, lnHTTP, lnTLS); close(done) }()
	t.Cleanup(func() { cancel(); <-done })
	return s, lnHTTP.Addr().String(), lnTLS.Addr().String()
}

// closedByPeer reports whether the server closed conn without sending anything.
func closedByPeer(t *testing.T, conn net.Conn) bool {
	t.Helper()
	_ = conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	n, err := conn.Read(make([]byte, 1))
	return n == 0 && err == io.EOF
}

func TestACLRefusesBeforeRead(t *testing.T) {
	s, _, tlsAddr := startProxy(t, snapshot.ProxySnap{
		SessionLimit: 10,
		Deny:         []string{"127.0.0.0/8"},
		Names:        []snapshot.NameRule{{Name: "example.com", Match: snapshot.MatchSuffix}},
	})
	conn, err := net.Dial("tcp", tlsAddr)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if !closedByPeer(t, conn) {
		t.Fatal("клиент из blacklist не отключён")
	}
	select {
	case r := <-s.Reports():
		if r.Status != "acl" {
			t.Fatalf("отчёт: %+v", r)
		}
	case <-time.After(time.Second):
		t.Fatal("нет отчёта")
	}
}

func TestGlobalConnCap(t *testing.T) {
	s, _, tlsAddr := startProxy(t, snapshot.ProxySnap{SessionLimit: 10})
	s.SetMaxConns(1)
	first, err := net.Dial("tcp", tlsAddr)
	if err != nil {
		t.Fatal(err)
	}
	defer first.Close()
	// The first connection waits in the ClientHello read; give the accept loop time to count it.
	time.Sleep(100 * time.Millisecond)
	second, err := net.Dial("tcp", tlsAddr)
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()
	if !closedByPeer(t, second) {
		t.Fatal("сверх лимита соединение принято")
	}
}

// Every request on a keep-alive connection is checked, not only the first one.
func TestHTTPHostPerRequest(t *testing.T) {
	_, httpAddr, _ := startProxy(t, snapshot.ProxySnap{
		SessionLimit: 10,
		Names:        []snapshot.NameRule{{Name: "allowed.test", Match: snapshot.MatchFQDN}},
		// Nothing listens here, so the allowed request fails to resolve: 502, connection kept.
		Upstreams: []string{"127.0.0.1:1"},
	})
	conn, err := net.Dial("tcp", httpAddr)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	br := bufio.NewReader(conn)
	send := func(host string) int {
		t.Helper()
		_ = conn.SetDeadline(time.Now().Add(15 * time.Second))
		if _, err := io.WriteString(conn, "GET / HTTP/1.1\r\nHost: "+host+"\r\n\r\n"); err != nil {
			t.Fatal(err)
		}
		resp, err := http.ReadResponse(br, nil)
		if err != nil {
			t.Fatal(err)
		}
		_, _ = io.Copy(io.Discard, resp.Body)
		resp.Body.Close()
		return resp.StatusCode
	}
	if code := send("allowed.test"); code != http.StatusBadGateway {
		t.Fatalf("разрешённое имя: %d", code)
	}
	if code := send("other.test"); code != http.StatusForbidden {
		t.Fatalf("чужой Host на том же соединении: %d", code)
	}
}

func TestHTTPDropsForwardedHeaders(t *testing.T) {
	var got http.Header
	origin := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r.Header.Clone()
		_, _ = io.WriteString(w, "ok")
	})}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	go func() { _ = origin.Serve(ln) }()
	defer origin.Close()

	s := New(nil, "", "")
	if err := s.SetSnapshot(&snapshot.ProxySnap{SessionLimit: 10, Names: []snapshot.NameRule{{Name: "allowed.test", Match: snapshot.MatchFQDN}}}); err != nil {
		t.Fatal(err)
	}
	// Loopback origins are blocked in production; dial the test origin directly.
	s.transport.DialContext = func(ctx context.Context, _, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, "tcp", ln.Addr().String())
	}
	req, _ := http.NewRequest(http.MethodGet, "http://allowed.test/", nil)
	req = req.WithContext(context.WithValue(req.Context(), connKey{}, &gatedConn{client: netip.MustParseAddr("192.0.2.1")}))
	req.Header.Set("X-Forwarded-For", "10.0.0.1")
	req.Header.Set("Forwarded", "for=10.0.0.1")
	rec := &recorder{header: http.Header{}}
	s.serveHTTP(rec, req)
	if rec.code != 0 && rec.code != http.StatusOK {
		t.Fatalf("status %d", rec.code)
	}
	if got == nil {
		t.Fatal("запрос не дошёл до origin")
	}
	for _, h := range []string{"X-Forwarded-For", "Forwarded", "X-Forwarded-Host", "X-Forwarded-Proto"} {
		if v := got.Get(h); v != "" {
			t.Fatalf("%s ушёл на origin: %q", h, v)
		}
	}
}

type recorder struct {
	header http.Header
	code   int
	body   strings.Builder
}

func (r *recorder) Header() http.Header         { return r.header }
func (r *recorder) WriteHeader(code int)        { r.code = code }
func (r *recorder) Write(p []byte) (int, error) { return r.body.Write(p) }

// A long download with a silent client must survive past the idle timeout.
func TestSpliceOneWayStream(t *testing.T) {
	clientSide, proxyClient := net.Pipe()
	proxyOrigin, originSide := net.Pipe()
	idle := 200 * time.Millisecond
	done := make(chan int64, 1)
	go func() {
		_, down := splice(proxyClient, proxyOrigin, idle)
		done <- down
	}()
	go func() { _, _ = io.Copy(io.Discard, clientSide) }()
	start := time.Now()
	for time.Since(start) < 4*idle {
		if _, err := originSide.Write([]byte("x")); err != nil {
			t.Fatalf("поток оборвался через %v: %v", time.Since(start), err)
		}
		time.Sleep(idle / 4)
	}
	// Now both sides are silent: the watchdog must close the session.
	select {
	case down := <-done:
		if down == 0 {
			t.Fatal("байты не посчитаны")
		}
	case <-time.After(5 * idle):
		t.Fatal("тихая сессия не закрылась")
	}
}

func TestClientKeyIPv6Slash64(t *testing.T) {
	a := clientKey(addrOf(&net.TCPAddr{IP: net.ParseIP("2001:db8:1:2::1")}))
	b := clientKey(addrOf(&net.TCPAddr{IP: net.ParseIP("2001:db8:1:2:ffff::9")}))
	c := clientKey(addrOf(&net.TCPAddr{IP: net.ParseIP("2001:db8:1:3::1")}))
	if a != b || a == c {
		t.Fatalf("a=%s b=%s c=%s", a, b, c)
	}
}
