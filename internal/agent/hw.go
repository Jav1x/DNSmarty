package agent

import (
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
)

const gib = 1 << 30

type IfaceStat struct {
	Name   string   `json:"name"`
	IP     string   `json:"ip,omitempty"`
	RxMbps *float64 `json:"rx_mbps"`
	TxMbps *float64 `json:"tx_mbps"`
}

type HW struct {
	CPUModel    string     `json:"cpu_model,omitempty"`
	CPUCores    int        `json:"cpu_cores,omitempty"`
	MemTotalMB  int64      `json:"mem_total_mb,omitempty"`
	MemUsedPct  *float64   `json:"mem_used_pct"`
	OS          string     `json:"os,omitempty"`
	Kernel      string     `json:"kernel,omitempty"`
	DiskTotalGB *float64   `json:"disk_total_gb"`
	DiskUsedGB  *float64   `json:"disk_used_gb"`
	UptimeSec   *int64     `json:"uptime_sec"`
	Iface       *IfaceStat `json:"iface,omitempty"`
}

var hwNow hwState

// CollectHW reads host hardware under root ("/" in production). Rates need two calls; the first reports null.
func CollectHW(root string) *HW {
	return hwNow.collect(root, time.Now())
}

type hwState struct {
	mu    sync.Mutex
	iface string
	rx    uint64
	tx    uint64
	at    time.Time
}

func (s *hwState) collect(root string, now time.Time) *HW {
	s.mu.Lock()
	defer s.mu.Unlock()
	hw := &HW{}
	found := readStatic(root, hw)
	if name, ok := defaultIface(root); ok {
		found = true
		st := &IfaceStat{Name: name}
		if root == "/" {
			st.IP = ifaceIPv4(name)
		}
		if rx, tx, ok := readCounters(root, name); ok {
			if s.iface == name && !s.at.IsZero() && now.After(s.at) && rx >= s.rx && tx >= s.tx {
				secs := now.Sub(s.at).Seconds()
				rxMbps := float64(rx-s.rx) * 8 / 1e6 / secs
				txMbps := float64(tx-s.tx) * 8 / 1e6 / secs
				st.RxMbps, st.TxMbps = &rxMbps, &txMbps
			}
			s.iface, s.rx, s.tx, s.at = name, rx, tx, now
		}
		hw.Iface = st
	}
	if !found {
		return nil
	}
	return hw
}

func readStatic(root string, hw *HW) bool {
	found := false
	if s, ok := readFile(root, "proc/cpuinfo"); ok {
		found = true
		hw.CPUModel, hw.CPUCores = parseCPU(s)
	}
	if s, ok := readFile(root, "proc/meminfo"); ok {
		found = true
		hw.MemTotalMB, hw.MemUsedPct = parseMem(s)
	}
	if s, ok := readFile(root, "proc/uptime"); ok {
		found = true
		if f := strings.Fields(s); len(f) > 0 {
			if v, err := strconv.ParseFloat(f[0], 64); err == nil {
				up := int64(v)
				hw.UptimeSec = &up
			}
		}
	}
	if s, ok := readFile(root, "etc/os-release"); ok {
		found = true
		hw.OS = parseOSRelease(s)
	}
	if s, ok := readFile(root, "proc/sys/kernel/osrelease"); ok {
		found = true
		hw.Kernel = strings.TrimSpace(s)
	}
	if root == "/" {
		if total, used, ok := diskUsage("/"); ok {
			found = true
			hw.DiskTotalGB, hw.DiskUsedGB = &total, &used
		}
	}
	return found
}

func readFile(root, rel string) (string, bool) {
	b, err := os.ReadFile(filepath.Join(root, rel))
	if err != nil {
		return "", false
	}
	return string(b), true
}

func parseCPU(s string) (model string, cores int) {
	for _, line := range strings.Split(s, "\n") {
		k, v, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		switch strings.TrimSpace(k) {
		case "processor":
			cores++
		case "model name":
			if model == "" {
				model = strings.TrimSpace(v)
			}
		}
	}
	return model, cores
}

func parseMem(s string) (totalMB int64, usedPct *float64) {
	var total, avail float64
	var hasAvail bool
	for _, line := range strings.Split(s, "\n") {
		k, v, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		f := strings.Fields(v)
		if len(f) == 0 {
			continue
		}
		kb, err := strconv.ParseFloat(f[0], 64)
		if err != nil {
			continue
		}
		switch strings.TrimSpace(k) {
		case "MemTotal":
			total = kb
		case "MemAvailable":
			avail, hasAvail = kb, true
		}
	}
	if total <= 0 {
		return 0, nil
	}
	totalMB = int64(total / 1024)
	if hasAvail {
		p := (total - avail) / total * 100
		usedPct = &p
	}
	return totalMB, usedPct
}

func parseOSRelease(s string) string {
	for _, line := range strings.Split(s, "\n") {
		if v, ok := strings.CutPrefix(line, "PRETTY_NAME="); ok {
			return strings.Trim(strings.TrimSpace(v), `"`)
		}
	}
	return ""
}

func diskUsage(path string) (totalGB, usedGB float64, ok bool) {
	var st syscall.Statfs_t
	if err := syscall.Statfs(path, &st); err != nil {
		return 0, 0, false
	}
	bs := float64(st.Bsize)
	return float64(st.Blocks) * bs / gib, float64(st.Blocks-st.Bfree) * bs / gib, true
}

func defaultIface(root string) (string, bool) {
	s, ok := readFile(root, "proc/net/route")
	if !ok {
		return "", false
	}
	for _, line := range strings.Split(s, "\n")[1:] {
		f := strings.Fields(line)
		if len(f) >= 2 && f[1] == "00000000" {
			return f[0], true
		}
	}
	return "", false
}

func readCounters(root, name string) (rx, tx uint64, ok bool) {
	s, found := readFile(root, "proc/net/dev")
	if !found {
		return 0, 0, false
	}
	for _, line := range strings.Split(s, "\n") {
		k, v, cut := strings.Cut(line, ":")
		if !cut || strings.TrimSpace(k) != name {
			continue
		}
		f := strings.Fields(v)
		if len(f) < 9 {
			return 0, 0, false
		}
		r, err1 := strconv.ParseUint(f[0], 10, 64)
		t, err2 := strconv.ParseUint(f[8], 10, 64)
		if err1 != nil || err2 != nil {
			return 0, 0, false
		}
		return r, t, true
	}
	return 0, 0, false
}

func ifaceIPv4(name string) string {
	ifc, err := net.InterfaceByName(name)
	if err != nil {
		return ""
	}
	addrs, err := ifc.Addrs()
	if err != nil {
		return ""
	}
	for _, a := range addrs {
		if ipn, ok := a.(*net.IPNet); ok && ipn.IP.To4() != nil {
			return ipn.IP.String()
		}
	}
	return ""
}
