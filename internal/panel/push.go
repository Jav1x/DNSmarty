package panel

import (
	"context"
	"encoding/json"
	"sync"
	"time"

	"dnsmarty/internal/agent"
	"dnsmarty/internal/snapshot"
	"dnsmarty/internal/store"
)

const (
	// pushParallel bounds concurrent agents per wave. One slow node no longer delays the rest.
	pushParallel = 8
	nodeTimeout  = 15 * time.Second
)

func (s *Server) PushLoop(ctx context.Context) {
	interval := 5 * time.Second
	timer := time.NewTimer(interval)
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
			if st, err := s.store.Settings(ctx); err == nil && st.PullIntervalSec >= 2 {
				interval = time.Duration(st.PullIntervalSec) * time.Second
			}
			s.pushAll(ctx)
			timer.Reset(interval)
		}
	}
}

func (s *Server) pushAll(ctx context.Context) {
	nodes, err := s.store.ListNodes(ctx)
	if err != nil {
		s.log.Warn("push list", "err", err)
		return
	}
	// Proxies first: a DNS snapshot only lists proxies seen within the live window,
	// so they must be refreshed before DNS nodes take their snapshot.
	var proxies, resolvers []store.Node
	for _, n := range nodes {
		if n.Role == snapshot.RoleProxy {
			proxies = append(proxies, n)
		} else {
			resolvers = append(resolvers, n)
		}
	}
	s.syncWave(ctx, proxies)
	s.syncWave(ctx, resolvers)
}

func (s *Server) syncWave(ctx context.Context, nodes []store.Node) {
	sem := make(chan struct{}, pushParallel)
	var wg sync.WaitGroup
	for _, n := range nodes {
		if ctx.Err() != nil {
			break
		}
		sem <- struct{}{}
		wg.Add(1)
		go func(n store.Node) {
			defer wg.Done()
			defer func() { <-sem }()
			nctx, cancel := context.WithTimeout(ctx, nodeTimeout)
			defer cancel()
			if err := s.syncNode(nctx, n.ID); err != nil {
				s.log.Warn("push", "node", n.Name, "err", err)
			}
		}(n)
	}
	wg.Wait()
}

func (s *Server) syncNode(ctx context.Context, id string) error {
	n, err := s.store.GetNode(ctx, id)
	if err != nil {
		return err
	}
	key, err := s.store.NodeKey(ctx, id)
	if err != nil {
		return err
	}
	t := agent.Target{ID: n.ID, Host: n.AgentHost, Port: n.AgentPort, Key: key}
	health, err := s.agents.Check(ctx, t)
	if err != nil {
		s.markUnreachable(ctx, id, err)
		return err
	}
	version, err := s.pushConfig(ctx, n, t)
	if err != nil {
		s.markUnreachable(ctx, id, err)
		return err
	}
	stats, err := s.agents.FetchStats(ctx, t)
	if err != nil {
		s.markUnreachable(ctx, id, err)
		return err
	}
	s.storeHW(ctx, id, stats.HW)
	if err := s.storeStats(ctx, n, stats); err != nil {
		s.log.Warn("stats", "node", n.Name, "err", err)
	}
	return s.store.SetReachable(ctx, id, version, health.Version)
}

func (s *Server) storeHW(ctx context.Context, id string, hw *agent.HW) {
	var raw json.RawMessage
	if hw != nil {
		b, err := json.Marshal(hw)
		if err != nil {
			s.log.Warn("hw", "node", id, "err", err)
			return
		}
		raw = b
	}
	if err := s.store.SetNodeHW(ctx, id, raw); err != nil {
		s.log.Warn("hw", "node", id, "err", err)
	}
}

// markUnreachable records the error even when ctx has already expired.
func (s *Server) markUnreachable(ctx context.Context, id string, cause error) {
	wctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	if err := s.store.SetUnreachable(wctx, id, cause.Error()); err != nil {
		s.log.Warn("node state", "node", id, "err", err)
	}
}

func (s *Server) pushConfig(ctx context.Context, n store.Node, t agent.Target) (int64, error) {
	version, err := s.pushOnce(ctx, n, t)
	have, stale := agent.IsStale(err)
	if !stale {
		return version, err
	}
	latest, lerr := s.store.LatestSnapshotVersion(ctx, n.Role, n.ID)
	if lerr != nil {
		return 0, lerr
	}
	if have <= latest {
		// A concurrent push already delivered a newer snapshot from this database.
		return have, nil
	}
	// The agent holds a version this database never published: it was restored or recreated.
	// A new epoch makes the agent accept our numbering again.
	s.log.Warn("snapshot epoch", "node", n.Name, "agent", have, "panel", latest)
	if err := s.store.RotateSnapshotEpoch(ctx); err != nil {
		return 0, err
	}
	return s.pushOnce(ctx, n, t)
}

func (s *Server) pushOnce(ctx context.Context, n store.Node, t agent.Target) (int64, error) {
	switch n.Role {
	case snapshot.RoleDNS:
		snap, err := s.store.DNSSnapshot(ctx)
		if err != nil {
			return 0, err
		}
		body := struct {
			snapshot.DNS
			NodeEnabled bool `json:"node_enabled"`
		}{DNS: snap, NodeEnabled: n.Enabled}
		if err := s.agents.Push(ctx, t, body); err != nil {
			return 0, err
		}
		return snap.Version, nil
	case snapshot.RoleProxy:
		snap, err := s.store.ProxySnapshot(ctx, n.ID)
		if err != nil {
			return 0, err
		}
		if err := s.agents.Push(ctx, t, snap); err != nil {
			return 0, err
		}
		return snap.Version, nil
	default:
		return 0, store.ErrInvalid
	}
}

func (s *Server) storeStats(ctx context.Context, n store.Node, stats agent.StatsBody) error {
	hits := make([]store.DNSHit, 0, len(stats.Hits))
	for _, h := range stats.Hits {
		hits = append(hits, store.DNSHit{
			At: h.At, ClientIP: h.ClientIP, QName: h.QName, QType: h.QType, Rcode: h.Rcode, Decision: h.Decision, LatencyMS: h.LatencyMS,
		})
	}
	skippedHits, err := s.store.InsertHits(ctx, n.ID, hits)
	if err != nil {
		return err
	}
	sessions := make([]store.ProxyReport, 0, len(stats.Sessions))
	for _, h := range stats.Sessions {
		sessions = append(sessions, store.ProxyReport{
			At: h.At, ClientIP: h.ClientIP, SNI: h.SNI, BytesUp: h.BytesUp, BytesDown: h.BytesDown, Status: h.Status, DialError: h.DialError,
		})
	}
	skippedSessions, err := s.store.InsertSessions(ctx, n.ID, sessions)
	if skippedHits+skippedSessions > 0 {
		s.log.Warn("stats", "node", n.Name, "skipped_hits", skippedHits, "skipped_sessions", skippedSessions)
	}
	return err
}
