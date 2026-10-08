package agent

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strconv"
	"sync"
	"time"

	"dnsmarty/internal/dns"
	"dnsmarty/internal/proxy"
)

// Target is one agent as the panel sees it.
type Target struct {
	ID   string
	Host string
	Port int
	Key  []byte
}

func (t Target) addr() string { return net.JoinHostPort(t.Host, strconv.Itoa(t.Port)) }

func (t Target) url(path string) string { return "https://" + t.addr() + path }

// Health is the agent's /health answer.
type Health struct {
	OK       bool   `json:"ok"`
	Role     string `json:"role"`
	Version  string `json:"version"`
	Snapshot int64  `json:"snapshot"`
}

// StaleError means the agent already runs a newer snapshot from the same epoch.
type StaleError struct {
	Have int64
}

func (e *StaleError) Error() string {
	return fmt.Sprintf("у агента снимок новее: %d", e.Have)
}

// Pool keeps one HTTP client per node so keep-alive connections are reused across push cycles.
type Pool struct {
	mu sync.Mutex
	m  map[string]poolEntry
}

type poolEntry struct {
	sum [32]byte
	c   *http.Client
}

func NewPool() *Pool {
	return &Pool{m: map[string]poolEntry{}}
}

func (p *Pool) client(t Target) (*http.Client, error) {
	sum := sha256.Sum256(t.Key)
	p.mu.Lock()
	defer p.mu.Unlock()
	if e, ok := p.m[t.ID]; ok {
		if e.sum == sum {
			return e.c, nil
		}
		e.c.CloseIdleConnections()
	}
	cfg, err := ClientTLS(t.Key)
	if err != nil {
		return nil, err
	}
	c := &http.Client{
		Timeout: 8 * time.Second,
		Transport: &http.Transport{
			TLSClientConfig:       cfg,
			TLSHandshakeTimeout:   5 * time.Second,
			ResponseHeaderTimeout: 8 * time.Second,
			// Shorter than the agent's IdleTimeout, so the panel never reuses a connection the agent is closing.
			IdleConnTimeout:     60 * time.Second,
			MaxIdleConnsPerHost: 2,
		},
	}
	p.m[t.ID] = poolEntry{sum: sum, c: c}
	return c, nil
}

// Forget drops the client of a deleted node or a node with a new key.
func (p *Pool) Forget(id string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if e, ok := p.m[id]; ok {
		e.c.CloseIdleConnections()
		delete(p.m, id)
	}
}

func (p *Pool) do(ctx context.Context, t Target, method, path string, body []byte) (*http.Response, error) {
	c, err := p.client(t)
	if err != nil {
		return nil, err
	}
	var rd io.Reader
	if body != nil {
		rd = bytes.NewReader(body)
	}
	req, err := http.NewRequestWithContext(ctx, method, t.url(path), rd)
	if err != nil {
		return nil, err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.Do(req)
	if err != nil {
		return nil, fmt.Errorf("нет связи с %s: %w", t.addr(), err)
	}
	return resp, nil
}

// drain reads what is left so the connection can go back to the pool.
func drain(resp *http.Response) {
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 64<<10))
	_ = resp.Body.Close()
}

func (p *Pool) Check(ctx context.Context, t Target) (Health, error) {
	var h Health
	resp, err := p.do(ctx, t, http.MethodGet, "/health", nil)
	if err != nil {
		return h, err
	}
	defer drain(resp)
	if resp.StatusCode != http.StatusOK {
		return h, fmt.Errorf("агент ответил %s", resp.Status)
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 64<<10)).Decode(&h); err != nil {
		return h, fmt.Errorf("агент ответил не JSON: %w", err)
	}
	return h, nil
}

func (p *Pool) Push(ctx context.Context, t Target, body any) error {
	raw, err := json.Marshal(body)
	if err != nil {
		return err
	}
	resp, err := p.do(ctx, t, http.MethodPost, "/config", raw)
	if err != nil {
		return err
	}
	defer drain(resp)
	switch resp.StatusCode {
	case http.StatusOK:
		return nil
	case http.StatusConflict:
		var out struct {
			Version int64 `json:"version"`
		}
		if err := json.NewDecoder(io.LimitReader(resp.Body, 4<<10)).Decode(&out); err != nil {
			return fmt.Errorf("конфиг не принят: %s", resp.Status)
		}
		return &StaleError{Have: out.Version}
	default:
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return fmt.Errorf("конфиг не принят: %s %s", resp.Status, bytes.TrimSpace(b))
	}
}

type StatsBody struct {
	Hits     []dns.Hit      `json:"hits"`
	Sessions []proxy.Report `json:"sessions"`
}

func (p *Pool) FetchStats(ctx context.Context, t Target) (StatsBody, error) {
	var out StatsBody
	resp, err := p.do(ctx, t, http.MethodGet, "/stats", nil)
	if err != nil {
		return out, err
	}
	defer drain(resp)
	if resp.StatusCode != http.StatusOK {
		return out, fmt.Errorf("статистика: %s", resp.Status)
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 32<<20)).Decode(&out); err != nil {
		return out, err
	}
	return out, nil
}

// IsStale reports whether err says the agent already has a newer snapshot.
func IsStale(err error) (int64, bool) {
	var s *StaleError
	if errors.As(err, &s) {
		return s.Have, true
	}
	return 0, false
}
