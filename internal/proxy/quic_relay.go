package proxy

import (
	"context"
	"encoding/hex"
	"errors"
	"net"
	"net/netip"
	"sync"
	"sync/atomic"
	"time"

	"dnsmarty/internal/metrics"
	"dnsmarty/internal/netx"
	"dnsmarty/internal/snapshot"
)

const (
	quicAmpFactor = 3
	quicHoldMax   = 8
	quicHoldTTL   = 3 * time.Second
)

type quicHold struct {
	pkts [][]byte
	at   time.Time
}

type quicSess struct {
	origin   net.Conn
	client   net.Addr
	sni      string
	clientIP string
	key      string
	idle     time.Duration
	up       atomic.Int64
	down     atomic.Int64
	in       atomic.Int64
	pkts     atomic.Int64
	once     sync.Once
	s        *Server
}

func (s *Server) serveQUIC(ctx context.Context, pc net.PacketConn) error {
	go func() {
		<-ctx.Done()
		_ = pc.Close()
	}()
	buf := make([]byte, 65535)
	for {
		n, addr, err := pc.ReadFrom(buf)
		if err != nil {
			s.closeQUIC()
			if errors.Is(err, net.ErrClosed) {
				return nil
			}
			return err
		}
		pkt := append([]byte(nil), buf[:n]...)
		s.handleQUIC(ctx, pc, addr, pkt)
	}
}

func (s *Server) closeQUIC() {
	s.qmu.Lock()
	all := make([]*quicSess, 0, len(s.quic))
	for _, q := range s.quic {
		all = append(all, q)
	}
	s.quic = map[string]*quicSess{}
	s.qhold = map[string]*quicHold{}
	s.qmu.Unlock()
	for _, q := range all {
		q.shutdown()
	}
}

func (s *Server) takeHold(key string, pkt []byte) [][]byte {
	s.qmu.Lock()
	defer s.qmu.Unlock()
	h := s.qhold[key]
	if h == nil || time.Since(h.at) > quicHoldTTL {
		delete(s.qhold, key)
		return [][]byte{pkt}
	}
	return append(append([][]byte{}, h.pkts...), pkt)
}

func (s *Server) saveHold(key string, pkts [][]byte) {
	if len(pkts) > quicHoldMax {
		pkts = pkts[len(pkts)-quicHoldMax:]
	}
	s.qmu.Lock()
	s.qhold[key] = &quicHold{pkts: pkts, at: time.Now()}
	s.qmu.Unlock()
}

func (s *Server) dropHold(key string) {
	s.qmu.Lock()
	delete(s.qhold, key)
	s.qmu.Unlock()
}

func (s *Server) handleQUIC(ctx context.Context, pc net.PacketConn, addr net.Addr, pkt []byte) {
	key := addr.String()
	s.qmu.Lock()
	if q := s.quic[key]; q != nil {
		s.qmu.Unlock()
		q.send(pkt)
		return
	}
	s.qmu.Unlock()

	client := addrOf(addr)
	c := s.snap.Load()
	if c == nil {
		s.logQUIC(addr, pkt, "", "no snapshot", nil)
		s.emit(Report{At: time.Now().UTC(), ClientIP: client.String(), Status: "refused", DialError: "no snapshot"})
		return
	}
	if !c.acl.Allowed(client) {
		s.logQUIC(addr, pkt, "", "acl", nil)
		s.refuseACL(client)
		return
	}
	pkts := s.takeHold(key, pkt)
	name, err := sniFromInitials(pkts)
	if err != nil {
		if _, ok := cryptoFragsFromPkt(pkt); ok {
			s.saveHold(key, pkts)
			s.logQUIC(addr, pkt, "", "hold", nil)
			return
		}
		s.dropHold(key)
		s.logQUIC(addr, pkt, "", "parse", err)
		if len(pkt) >= 1200 {
			s.noteQUICDrop(client, pkt, err)
		}
		return
	}
	s.dropHold(key)
	if !nameAllowed(name, c.snap.Names) {
		s.logQUIC(addr, pkt, name, "not in snapshot", nil)
		s.emit(Report{At: time.Now().UTC(), ClientIP: client.String(), SNI: clip(name, 255), Status: "refused", DialError: "not in snapshot"})
		return
	}
	if s.conns.Add(1) > s.maxConns.Load() {
		s.conns.Add(-1)
		s.logQUIC(addr, pkt, name, "overload", nil)
		metrics.ProxySessions.WithLabelValues("overload").Inc()
		return
	}
	sessKey := clientKey(client)
	limit := c.snap.SessionLimit
	if limit < 1 {
		limit = 1
	}
	if !s.acquire(sessKey, limit) {
		s.conns.Add(-1)
		s.logQUIC(addr, pkt, name, "limited", nil)
		s.emit(Report{At: time.Now().UTC(), ClientIP: client.String(), SNI: clip(name, 255), Status: "limited"})
		return
	}
	origin, err := s.dialOriginUDP(ctx, c.snap, snapshot.Normalize(name))
	if err != nil {
		s.release(sessKey)
		s.conns.Add(-1)
		s.logQUIC(addr, pkt, name, "dial", err)
		s.emit(Report{At: time.Now().UTC(), ClientIP: client.String(), SNI: clip(name, 255), Status: "dial_error", DialError: clip(err.Error(), 300)})
		return
	}
	q := &quicSess{
		origin:   origin,
		client:   addr,
		sni:      snapshot.Normalize(name),
		clientIP: client.String(),
		key:      sessKey,
		idle:     idleTimeout(c.snap),
		s:        s,
	}
	s.qmu.Lock()
	if other := s.quic[key]; other != nil {
		s.qmu.Unlock()
		_ = origin.Close()
		s.release(sessKey)
		s.conns.Add(-1)
		for _, p := range pkts {
			other.send(p)
		}
		return
	}
	s.quic[key] = q
	s.qmu.Unlock()
	s.logQUIC(addr, pkt, q.sni, "open", nil)
	for _, p := range pkts {
		q.send(p)
	}
	go q.pump(pc)
}

func (q *quicSess) send(pkt []byte) {
	q.pkts.Add(1)
	q.in.Add(int64(len(pkt)))
	q.up.Add(int64(len(pkt)))
	now := time.Now()
	_ = q.origin.SetWriteDeadline(now.Add(5 * time.Second))
	_ = q.origin.SetReadDeadline(now.Add(q.idle))
	if _, err := q.origin.Write(pkt); err != nil {
		q.shutdown()
	}
}

func (q *quicSess) pump(pc net.PacketConn) {
	defer q.shutdown()
	buf := make([]byte, 65535)
	first := true
	for {
		_ = q.origin.SetReadDeadline(time.Now().Add(q.idle))
		n, err := q.origin.Read(buf)
		if err != nil {
			if first {
				q.s.log.Info("quic", "result", "origin idle", "sni", q.sni, "client", q.client.String(), "err", err)
			}
			return
		}
		if first {
			first = false
			q.s.log.Info("quic", "result", "origin", "sni", q.sni, "n", n, "client", q.client.String())
		}
		out := buf[:n]
		if !q.validated() {
			max := q.in.Load() * quicAmpFactor
			already := q.down.Load()
			if already >= max {
				continue
			}
			if already+int64(len(out)) > max {
				out = out[:max-already]
			}
			if len(out) == 0 {
				continue
			}
		}
		if _, err := pc.WriteTo(out, q.client); err != nil {
			return
		}
		q.down.Add(int64(len(out)))
	}
}

func (q *quicSess) validated() bool {
	return q.pkts.Load() >= 2 || q.in.Load() >= 1200
}

func (q *quicSess) shutdown() {
	q.once.Do(func() {
		_ = q.origin.Close()
		q.s.qmu.Lock()
		delete(q.s.quic, q.client.String())
		q.s.qmu.Unlock()
		q.s.release(q.key)
		q.s.conns.Add(-1)
		q.s.emit(Report{
			At:        time.Now().UTC(),
			ClientIP:  q.clientIP,
			SNI:       clip(q.sni, 255),
			BytesUp:   q.up.Load(),
			BytesDown: q.down.Load(),
			Status:    "ok",
		})
	})
}

func (s *Server) dialOriginUDP(ctx context.Context, snap *snapshot.ProxySnap, name string) (net.Conn, error) {
	if s.udpDial != nil {
		return s.udpDial(ctx, snap, name)
	}
	ips, err := s.resolveCached(name, snap.Upstreams)
	if err != nil {
		return nil, err
	}
	own := ownIPs(snap)
	dialTimeout := time.Duration(snap.DialTimeoutMs) * time.Millisecond
	if dialTimeout <= 0 {
		dialTimeout = 5 * time.Second
	}
	last := errors.New("no address")
	for _, ip := range ips {
		if netx.IsBlocked(ip, own) {
			last = errors.New("blocked address")
			continue
		}
		d := net.Dialer{Timeout: dialTimeout}
		c, derr := d.DialContext(ctx, "udp", net.JoinHostPort(ip.String(), "443"))
		if derr != nil {
			last = derr
			continue
		}
		return c, nil
	}
	return nil, last
}

func (s *Server) logQUIC(addr net.Addr, pkt []byte, sni, result string, err error) {
	head, ver := quicHead(pkt)
	args := []any{"src", addr.String(), "n", len(pkt), "ver", ver, "head", head, "result", result}
	if sni != "" {
		args = append(args, "sni", sni)
	}
	if err != nil {
		args = append(args, "err", err)
	}
	s.log.Info("quic", args...)
}

func (s *Server) noteQUICDrop(client netip.Addr, pkt []byte, err error) {
	ip := client.String()
	key := "q:" + ip
	now := time.Now()
	s.aclMu.Lock()
	last, seen := s.aclSeen[key]
	if !seen || now.Sub(last) > time.Minute {
		s.aclSeen[key] = now
		seen = false
	}
	s.aclMu.Unlock()
	if seen {
		return
	}
	n := 16
	if len(pkt) < n {
		n = len(pkt)
	}
	s.emit(Report{
		At:        now.UTC(),
		ClientIP:  ip,
		Status:    "refused",
		DialError: clip("quic "+hex.EncodeToString(pkt[:n])+" "+err.Error(), 300),
	})
}
