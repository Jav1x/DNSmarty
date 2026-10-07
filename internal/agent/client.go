package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"strconv"
	"time"

	"dnsmarty/internal/dns"
	"dnsmarty/internal/proxy"
)

func client(nodeKey []byte) (*http.Client, error) {
	cfg, err := ClientTLS(nodeKey)
	if err != nil {
		return nil, err
	}
	return &http.Client{
		Timeout: 8 * time.Second,
		Transport: &http.Transport{
			TLSClientConfig: cfg,
		},
	}, nil
}

func baseURL(host string, port int) string {
	return "https://" + net.JoinHostPort(host, strconv.Itoa(port))
}

func Check(ctx context.Context, host string, port int, nodeKey []byte) error {
	c, err := client(nodeKey)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, baseURL(host, port)+"/health", nil)
	if err != nil {
		return err
	}
	resp, err := c.Do(req)
	if err != nil {
		return fmt.Errorf("нет связи с %s: %w", net.JoinHostPort(host, strconv.Itoa(port)), err)
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, resp.Body)
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("агент ответил %s", resp.Status)
	}
	return nil
}

func Push(ctx context.Context, host string, port int, nodeKey []byte, body any) error {
	raw, err := json.Marshal(body)
	if err != nil {
		return err
	}
	c, err := client(nodeKey)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, baseURL(host, port)+"/config", bytes.NewReader(raw))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.Do(req)
	if err != nil {
		return fmt.Errorf("нет связи с %s: %w", net.JoinHostPort(host, strconv.Itoa(port)), err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return fmt.Errorf("конфиг не принят: %s %s", resp.Status, bytes.TrimSpace(b))
	}
	return nil
}

type StatsBody struct {
	Hits     []dns.Hit      `json:"hits"`
	Sessions []proxy.Report `json:"sessions"`
}

func FetchStats(ctx context.Context, host string, port int, nodeKey []byte) (StatsBody, error) {
	var out StatsBody
	c, err := client(nodeKey)
	if err != nil {
		return out, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, baseURL(host, port)+"/stats", nil)
	if err != nil {
		return out, err
	}
	resp, err := c.Do(req)
	if err != nil {
		return out, fmt.Errorf("нет связи с %s: %w", net.JoinHostPort(host, strconv.Itoa(port)), err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return out, fmt.Errorf("статистика: %s", resp.Status)
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return out, err
	}
	return out, nil
}
