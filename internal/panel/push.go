package panel

import (
	"context"
	"time"

	"dnsmarty/internal/agent"
	"dnsmarty/internal/snapshot"
	"dnsmarty/internal/store"
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
	for _, n := range nodes {
		if err := s.syncNode(ctx, n.ID); err != nil {
			s.log.Warn("push", "node", n.Name, "err", err)
		}
	}
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
	if err := agent.Check(ctx, n.AgentHost, n.AgentPort, key); err != nil {
		_ = s.store.SetUnreachable(ctx, id, err.Error())
		return err
	}
	version, err := s.pushConfig(ctx, n, key)
	if err != nil {
		_ = s.store.SetUnreachable(ctx, id, err.Error())
		return err
	}
	stats, err := agent.FetchStats(ctx, n.AgentHost, n.AgentPort, key)
	if err != nil {
		_ = s.store.SetUnreachable(ctx, id, err.Error())
		return err
	}
	if err := s.storeStats(ctx, n, stats); err != nil {
		s.log.Warn("stats", "node", n.Name, "err", err)
	}
	return s.store.SetReachable(ctx, id, version)
}

func (s *Server) pushConfig(ctx context.Context, n store.Node, key []byte) (int64, error) {
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
		if err := agent.Push(ctx, n.AgentHost, n.AgentPort, key, body); err != nil {
			return 0, err
		}
		return snap.Version, nil
	case snapshot.RoleProxy:
		snap, err := s.store.ProxySnapshot(ctx, n.ID)
		if err != nil {
			return 0, err
		}
		if err := agent.Push(ctx, n.AgentHost, n.AgentPort, key, snap); err != nil {
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
			At: h.At, ClientIP: h.ClientIP, QName: h.QName, QType: h.QType, Rcode: h.Rcode, Decision: h.Decision,
		})
	}
	if err := s.store.InsertHits(ctx, n.ID, hits); err != nil {
		return err
	}
	sessions := make([]store.ProxyReport, 0, len(stats.Sessions))
	for _, h := range stats.Sessions {
		sessions = append(sessions, store.ProxyReport{
			At: h.At, ClientIP: h.ClientIP, SNI: h.SNI, BytesUp: h.BytesUp, BytesDown: h.BytesDown, Status: h.Status, DialError: h.DialError,
		})
	}
	return s.store.InsertSessions(ctx, n.ID, sessions)
}
