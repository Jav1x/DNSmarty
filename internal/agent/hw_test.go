package agent

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestCollectHWFull(t *testing.T) {
	var s hwState
	hw := s.collect("testdata/full", time.Unix(1000, 0))
	if hw == nil {
		t.Fatal("nil hardware for full fixture")
	}
	if hw.CPUModel != "Test CPU 9000" || hw.CPUCores != 2 {
		t.Fatalf("cpu = %q/%d", hw.CPUModel, hw.CPUCores)
	}
	if hw.MemTotalMB != 8192 || hw.MemUsedPct == nil || *hw.MemUsedPct != 75 {
		t.Fatalf("mem = %d/%v", hw.MemTotalMB, hw.MemUsedPct)
	}
	if hw.OS != "Test OS 1.0" || hw.Kernel != "6.1.0-test" {
		t.Fatalf("os/kernel = %q/%q", hw.OS, hw.Kernel)
	}
	if hw.UptimeSec == nil || *hw.UptimeSec != 12345 {
		t.Fatalf("uptime = %v", hw.UptimeSec)
	}
	if hw.Iface == nil || hw.Iface.Name != "eth0" {
		t.Fatalf("iface = %+v", hw.Iface)
	}
	if hw.Iface.RxMbps != nil || hw.Iface.TxMbps != nil {
		t.Fatal("first call must report null rates")
	}
}

func TestMemWithoutAvailable(t *testing.T) {
	var s hwState
	hw := s.collect("testdata/noavail", time.Unix(1000, 0))
	if hw == nil {
		t.Fatal("nil hardware")
	}
	if hw.MemUsedPct != nil {
		t.Fatalf("mem_used_pct = %v, want nil without MemAvailable", *hw.MemUsedPct)
	}
	if hw.MemTotalMB != 8192 {
		t.Fatalf("mem_total_mb = %d", hw.MemTotalMB)
	}
}

func TestEmptyRootIsNil(t *testing.T) {
	if hw := CollectHW(t.TempDir()); hw != nil {
		t.Fatalf("hardware from empty root: %+v", hw)
	}
}

func TestInterfaceRatesFromCounterDelta(t *testing.T) {
	root := t.TempDir()
	write := func(rel, body string) {
		p := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("proc/net/route", "Iface Destination Gateway Flags\neth0 00000000 0102A8C0 0003\n")
	write("proc/net/dev", "  eth0: 1000000 0 0 0 0 0 0 0 500000 0 0 0 0 0 0 0\n")
	var s hwState
	t0 := time.Unix(1000, 0)
	s.collect(root, t0)
	write("proc/net/dev", "  eth0: 2000000 0 0 0 0 0 0 0 1000000 0 0 0 0 0 0 0\n")
	hw := s.collect(root, t0.Add(time.Second))
	if hw.Iface.RxMbps == nil || *hw.Iface.RxMbps != 8 {
		t.Fatalf("rx_mbps = %v, want 8", hw.Iface.RxMbps)
	}
	if hw.Iface.TxMbps == nil || *hw.Iface.TxMbps != 4 {
		t.Fatalf("tx_mbps = %v, want 4", hw.Iface.TxMbps)
	}
}
