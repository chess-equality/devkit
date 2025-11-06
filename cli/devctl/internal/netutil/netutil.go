package netutil

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"os/exec"
	"strings"
	"time"

	"devkit/cli/devctl/internal/execx"
)

type routeEntry struct {
	Dst string `json:"dst"`
}

const dnsHostOffset = 53

// PickInternalSubnet returns a non-overlapping /24 CIDR and a DNS IP reserved away from the first few auto-assigned addresses.
// Honors DEVKIT_INTERNAL_SUBNET and DEVKIT_DNS_IP if set. Falls back to defaults if detection fails.
func PickInternalSubnet() (cidr string, dnsIP string) {
	// Respect explicit overrides
	if v := strings.TrimSpace(os.Getenv("DEVKIT_INTERNAL_SUBNET")); v != "" {
		cidr = v
		if d := strings.TrimSpace(os.Getenv("DEVKIT_DNS_IP")); d != "" {
			dnsIP = d
		} else {
			dnsIP = dnsFromCIDR(cidr)
		}
		return
	}

	// Default candidates within 172.30.0.0/16
	candidates := []string{
		"172.30.10.0/24",
		"172.30.20.0/24",
		"172.30.30.0/24",
		"172.30.40.0/24",
		"172.30.50.0/24",
		"172.30.60.0/24",
		"172.30.70.0/24",
		"172.30.80.0/24",
		"172.30.90.0/24",
		"172.30.100.0/24",
	}

	used := getUsedCIDRs()
	for _, c := range candidates {
		if !overlapsAny(c, used) {
			cidr = c
			dnsIP = dnsFromCIDR(cidr)
			return
		}
	}
	// Fallback to default
	cidr = "172.30.10.0/24"
	dnsIP = dnsFromCIDR(cidr)
	return
}

func DNSFromCIDR(cidr string) string {
	return dnsFromCIDR(cidr)
}

func dnsFromCIDR(cidr string) string {
	ip, ipnet, err := net.ParseCIDR(cidr)
	if err != nil {
		return "172.30.10.53"
	}
	ip4 := ip.To4()
	if ip4 == nil {
		return ip.String()
	}

	base := binary.BigEndian.Uint32(ip4)
	candidate := make(net.IP, len(ip4))
	binary.BigEndian.PutUint32(candidate, base+dnsHostOffset)
	if ipnet.Contains(candidate) && validHostOctet(candidate) {
		return candidate.String()
	}

	fallback := make(net.IP, len(ip4))
	binary.BigEndian.PutUint32(fallback, base+3)
	if ipnet.Contains(fallback) && validHostOctet(fallback) {
		return fallback.String()
	}

	return ipnet.IP.String()
}

func validHostOctet(ip net.IP) bool {
	b := ip.To4()
	if b == nil {
		return true
	}
	octet := b[3]
	return octet != 0 && octet != 255
}

func overlapsAny(candidate string, used []string) bool {
	_, cn, err := net.ParseCIDR(candidate)
	if err != nil {
		return true
	}
	for _, u := range used {
		_, un, err := net.ParseCIDR(u)
		if err != nil {
			continue
		}
		if cidrOverlap(cn, un) {
			return true
		}
	}
	return false
}

// OverlapsAnyCIDR reports whether candidate overlaps with any entry in used.
func OverlapsAnyCIDR(candidate string, used []string) bool {
	return overlapsAny(candidate, used)
}

func cidrOverlap(a, b *net.IPNet) bool {
	return a.Contains(b.IP) || b.Contains(a.IP)
}

func getUsedCIDRs() []string {
	// Try `ip -j route` first
	out, err := exec.Command("ip", "-j", "route").Output()
	if err == nil {
		var entries []map[string]any
		if json.Unmarshal(out, &entries) == nil {
			var res []string
			for _, e := range entries {
				if dst, ok := e["dst"].(string); ok {
					if dst == "default" || !strings.Contains(dst, "/") {
						continue
					}
					res = append(res, dst)
				}
			}
			if len(res) > 0 {
				return append(res, getDockerNetworkCIDRs()...)
			}
		}
	}
	// Fallback: `ip route`
	out, err = exec.Command("ip", "route").Output()
	if err != nil {
		return getDockerNetworkCIDRs()
	}
	var res []string
	for _, line := range strings.Split(string(out), "\n") {
		fields := strings.Fields(line)
		if len(fields) == 0 {
			continue
		}
		if fields[0] == "default" {
			continue
		}
		if strings.Contains(fields[0], "/") {
			res = append(res, fields[0])
		}
	}
	return append(res, getDockerNetworkCIDRs()...)
}

func getDockerNetworkCIDRs() []string {
	out, err := exec.Command("docker", "network", "ls", "--format", "{{.ID}}").Output()
	if err != nil {
		return nil
	}
	lines := strings.Fields(strings.TrimSpace(string(out)))
	if len(lines) == 0 {
		return nil
	}
	cidrs := make([]string, 0, len(lines))
	for _, id := range lines {
		inspect, err := exec.Command("docker", "network", "inspect", id, "--format", "{{range .IPAM.Config}}{{.Subnet}}\n{{end}}").Output()
		if err != nil {
			continue
		}
		for _, line := range strings.Split(string(inspect), "\n") {
			line = strings.TrimSpace(line)
			if line == "" {
				continue
			}
			cidrs = append(cidrs, line)
		}
	}
	return cidrs
}

// UsedCIDRs exposes the discovered CIDR list (host + docker networks).
func UsedCIDRs() []string {
	return getUsedCIDRs()
}

// SubnetAvailable attempts to create a dummy network with the provided CIDR to detect conflicts.
// Returns true if the subnet can be used.
func SubnetAvailable(cidr string) bool {
	name := fmt.Sprintf("devkit-cidr-test-%d", time.Now().UnixNano())
	ctx, cancel := execx.WithTimeout(10 * time.Second)
	defer cancel()
	res := execx.RunCtx(ctx, "docker", "network", "create", "--driver", "bridge", "--subnet", cidr, name)
	if res.Code != 0 {
		return false
	}
	_ = execx.RunCtx(context.Background(), "docker", "network", "rm", name)
	return true
}
