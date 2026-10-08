package dns

import (
	"strings"
	"sync"
	"time"

	mdns "github.com/miekg/dns"

	"dnsmarty/internal/lru"
)

// cacheSize bounds the number of upstream answers kept: enough for a busy resolver
// without letting one spoofed-name flood evict real entries.
const cacheSize = 10000

// Negative answers (NXDOMAIN, NODATA) are cached briefly per RFC 2308, capped so a
// configuration mistake does not stick for the full SOA MINIMUM.
const negMaxTTL = 30 * time.Second

type cachedAnswer struct {
	msg     *mdns.Msg
	stored  time.Time
	expires time.Time
}

type fcall struct {
	done chan struct{}
	msg  *mdns.Msg // template, Id copied per caller
	err  error
}

// cache wraps the LRU, singleflight and TTL handling for forwarded answers.
type cache struct {
	mu     sync.Mutex
	store  *lru.Cache[string, *cachedAnswer]
	flyMu  sync.Mutex
	flight map[string]*fcall
}

func newCache() *cache {
	return &cache{store: lru.New[string, *cachedAnswer](cacheSize), flight: map[string]*fcall{}}
}

func cacheKey(qname string, qtype uint16) string {
	return strings.ToLower(strings.TrimSuffix(qname, ".")) + "|" + mdns.TypeToString[qtype]
}

// get returns a fresh copy with the request Id, the request's original question name
// (in case the upstream normalised case), and TTLs reduced by the time in the cache.
func (c *cache) get(key string, req *mdns.Msg, now time.Time) *mdns.Msg {
	c.mu.Lock()
	ca, ok := c.store.Get(key)
	c.mu.Unlock()
	if !ok || now.After(ca.expires) {
		return nil
	}
	out := ca.msg.Copy()
	out.Id = req.Id
	if len(out.Question) > 0 && len(req.Question) > 0 {
		out.Question[0].Name = req.Question[0].Name
	}
	decay(out, now.Sub(ca.stored))
	return out
}

func (c *cache) put(key string, msg *mdns.Msg, now time.Time) {
	ttl, ok := cacheTTL(msg)
	if !ok {
		return
	}
	c.mu.Lock()
	c.store.Add(key, &cachedAnswer{msg: msg.Copy(), stored: now, expires: now.Add(ttl)})
	c.mu.Unlock()
}

// flightDo runs fn once for a key, so concurrent identical queries share one upstream call.
func (c *cache) flightDo(key string, fn func() (*mdns.Msg, error)) (*mdns.Msg, error) {
	c.flyMu.Lock()
	if call, ok := c.flight[key]; ok {
		c.flyMu.Unlock()
		<-call.done
		if call.err != nil {
			return nil, call.err
		}
		return call.msg.Copy(), nil
	}
	call := &fcall{done: make(chan struct{})}
	c.flight[key] = call
	c.flyMu.Unlock()
	defer func() {
		c.flyMu.Lock()
		delete(c.flight, key)
		c.flyMu.Unlock()
		close(call.done)
	}()
	msg, err := fn()
	if err != nil {
		call.err = err
		return nil, err
	}
	call.msg = msg
	return msg.Copy(), nil
}

// cacheTTL is how long a response may be cached. Only successful answers, NXDOMAIN and NODATA
// are cacheable; SERVFAIL, REFUSED and zero-TTL answers are not.
func cacheTTL(m *mdns.Msg) (time.Duration, bool) {
	switch m.Rcode {
	case mdns.RcodeSuccess:
		if len(m.Answer) == 0 {
			return negativeTTL(m)
		}
		min := m.Answer[0].Header().Ttl
		for _, rr := range m.Answer[1:] {
			if t := rr.Header().Ttl; t < min {
				min = t
			}
		}
		if min == 0 {
			return 0, false
		}
		return clamp(time.Duration(min)*time.Second, time.Second, time.Hour), true
	case mdns.RcodeNameError:
		return negativeTTL(m)
	default:
		return 0, false
	}
}

// negativeTTL uses the SOA in the authority section: min(SOA TTL, SOA MINIMUM).
func negativeTTL(m *mdns.Msg) (time.Duration, bool) {
	for _, rr := range m.Ns {
		if soa, ok := rr.(*mdns.SOA); ok {
			t := soa.Hdr.Ttl
			if soa.Minttl > 0 && soa.Minttl < t {
				t = soa.Minttl
			}
			if t == 0 {
				return 0, false
			}
			return clamp(time.Duration(t)*time.Second, time.Second, negMaxTTL), true
		}
	}
	return 0, false
}

func clamp(d, lo, hi time.Duration) time.Duration {
	if d < lo {
		return lo
	}
	if d > hi {
		return hi
	}
	return d
}

// decay lowers every record TTL by elapsed; a cached answer served at 4 of its 5 s of life
// reports a TTL close to reality instead of the value the upstream returned.
func decay(m *mdns.Msg, elapsed time.Duration) {
	sub := uint32(elapsed / time.Second)
	for _, rr := range m.Answer {
		if t := rr.Header().Ttl; t > sub {
			rr.Header().Ttl = t - sub
		} else if t > 0 {
			rr.Header().Ttl = 1
		}
	}
	for _, rr := range m.Ns {
		if t := rr.Header().Ttl; t > sub {
			rr.Header().Ttl = t - sub
		} else if t > 0 {
			rr.Header().Ttl = 1
		}
	}
}
