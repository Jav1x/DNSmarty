package ratelimit

import (
	"net/netip"
	"sync"
	"sync/atomic"
	"time"
)

// Limiter is a token bucket per client network: /24 for IPv4, /56 for IPv6.
// A spoofed-source flood is spread over many addresses of one network, so per-network
// buckets cap what a reflection attack can extract from one victim prefix.
type Limiter struct {
	qps    atomic.Int64
	shards [16]shard
}

type shard struct {
	mu      sync.Mutex
	buckets map[string]*bucket
	lastGC  time.Time
}

type bucket struct {
	tokens  float64
	last    time.Time
	refused uint64
}

// Verdict says what to do with one query.
type Verdict int

const (
	Pass Verdict = iota
	// Slip answers with an empty truncated reply so a real client retries over TCP.
	Slip
	// Drop sends nothing.
	Drop
)

func New(qps int) *Limiter {
	l := &Limiter{}
	l.SetQPS(qps)
	return l
}

// SetQPS changes the rate; 0 turns limiting off. Burst is twice the rate.
func (l *Limiter) SetQPS(qps int) {
	if qps < 0 {
		qps = 0
	}
	l.qps.Store(int64(qps))
}

func (l *Limiter) Check(addr netip.Addr) Verdict {
	return l.check(addr, time.Now())
}

func (l *Limiter) check(addr netip.Addr, now time.Time) Verdict {
	qps := float64(l.qps.Load())
	if qps == 0 {
		return Pass
	}
	key, ok := prefix(addr)
	if !ok {
		return Pass
	}
	burst := 2 * qps
	sh := &l.shards[fnv(key)%uint32(len(l.shards))]
	sh.mu.Lock()
	defer sh.mu.Unlock()
	if sh.buckets == nil {
		sh.buckets = map[string]*bucket{}
	}
	if now.Sub(sh.lastGC) > time.Minute {
		for k, b := range sh.buckets {
			if now.Sub(b.last) > time.Minute {
				delete(sh.buckets, k)
			}
		}
		sh.lastGC = now
	}
	b := sh.buckets[key]
	if b == nil {
		b = &bucket{tokens: burst, last: now}
		sh.buckets[key] = b
	}
	b.tokens += now.Sub(b.last).Seconds() * qps
	if b.tokens > burst {
		b.tokens = burst
	}
	b.last = now
	if b.tokens >= 1 {
		b.tokens--
		return Pass
	}
	// Slip every second refusal, as in BIND RRL: legitimate clients still get a TC and retry over TCP.
	b.refused++
	if b.refused%2 == 0 {
		return Slip
	}
	return Drop
}

func prefix(addr netip.Addr) (string, bool) {
	addr = addr.Unmap()
	switch {
	case addr.Is4():
		b := addr.As4()
		return string(b[:3]), true
	case addr.Is6():
		b := addr.As16()
		return string(b[:7]), true
	default:
		return "", false
	}
}

func fnv(s string) uint32 {
	h := uint32(2166136261)
	for i := 0; i < len(s); i++ {
		h ^= uint32(s[i])
		h *= 16777619
	}
	return h
}
