package hostsync

import (
	"fmt"
	"sort"
	"strings"
)

// CollectIngressHosts returns deduplicated ingress hostnames in stable order.
func CollectIngressHosts(raw []string) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(raw))
	for _, host := range raw {
		trimmed := strings.TrimSpace(strings.ToLower(host))
		if trimmed == "" || seen[trimmed] {
			continue
		}
		seen[trimmed] = true
		out = append(out, trimmed)
	}
	sort.Strings(out)
	return out
}

func markerStart(project string) string {
	return fmt.Sprintf("# devkit:%s:ingress:start", sanitize(project))
}

func markerEnd(project string) string {
	return fmt.Sprintf("# devkit:%s:ingress:end", sanitize(project))
}

// RenderManagedBlock prints a deterministic hosts block for the given project.
func RenderManagedBlock(project string, ip string, hosts []string) string {
	entries := CollectIngressHosts(hosts)
	if len(entries) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString(markerStart(project))
	b.WriteString("\n")
	b.WriteString(strings.TrimSpace(ip))
	b.WriteString(" ")
	b.WriteString(strings.Join(entries, " "))
	b.WriteString("\n")
	b.WriteString(markerEnd(project))
	b.WriteString("\n")
	return b.String()
}

// UpsertManagedBlock inserts or replaces the managed block and returns updated content.
func UpsertManagedBlock(existing string, project string, ip string, hosts []string) (string, error) {
	block := RenderManagedBlock(project, ip, hosts)
	if strings.TrimSpace(block) == "" {
		return existing, nil
	}
	start := markerStart(project)
	end := markerEnd(project)
	startIdx := strings.Index(existing, start)
	endIdx := strings.Index(existing, end)
	if (startIdx >= 0 && endIdx < 0) || (startIdx < 0 && endIdx >= 0) {
		return "", fmt.Errorf("hosts block markers are incomplete for project %s", project)
	}
	if startIdx >= 0 && endIdx >= 0 {
		if endIdx < startIdx {
			return "", fmt.Errorf("hosts block markers are out of order for project %s", project)
		}
		endLine := endIdx + len(end)
		if endLine < len(existing) && existing[endLine] == '\n' {
			endLine++
		}
		return existing[:startIdx] + block + existing[endLine:], nil
	}

	trimmed := existing
	if trimmed != "" && !strings.HasSuffix(trimmed, "\n") {
		trimmed += "\n"
	}
	if strings.TrimSpace(trimmed) != "" {
		trimmed += "\n"
	}
	return trimmed + block, nil
}

// ParseHostMappings parses /etc/hosts style content into host->ip mappings.
func ParseHostMappings(content string) map[string]string {
	mappings := map[string]string{}
	lines := strings.Split(content, "\n")
	for _, line := range lines {
		raw := strings.TrimSpace(line)
		if raw == "" || strings.HasPrefix(raw, "#") {
			continue
		}
		fields := strings.Fields(raw)
		if len(fields) < 2 {
			continue
		}
		ip := fields[0]
		for _, host := range fields[1:] {
			trimmed := strings.TrimSpace(strings.ToLower(host))
			if trimmed == "" {
				continue
			}
			mappings[trimmed] = ip
		}
	}
	return mappings
}

// MissingMappings returns hosts that do not map to the expected IP.
func MissingMappings(content string, ip string, hosts []string) []string {
	expected := CollectIngressHosts(hosts)
	mappings := ParseHostMappings(content)
	missing := make([]string, 0, len(expected))
	for _, host := range expected {
		if mappings[host] != ip {
			missing = append(missing, host)
		}
	}
	return missing
}

func sanitize(project string) string {
	project = strings.TrimSpace(project)
	if project == "" {
		project = "default"
	}
	return strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z':
			return r
		case r >= 'A' && r <= 'Z':
			return r
		case r >= '0' && r <= '9':
			return r
		case r == '-' || r == '_':
			return r
		default:
			return '_'
		}
	}, project)
}
