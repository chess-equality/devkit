package allow

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"devkit/cli/devctl/internal/cmdregistry"
	fz "devkit/cli/devctl/internal/files"
)

// Register adds the allow command to the registry.
func Register(r *cmdregistry.Registry) {
	r.Register("allow", handle)
}

func handle(ctx *cmdregistry.Context) error {
	project := strings.TrimSpace(ctx.Project)
	if project == "" {
		return fmt.Errorf("-p <project> is required")
	}
	if len(ctx.Args) == 0 {
		return fmt.Errorf("allow requires <domain>")
	}
	domain := strings.TrimSpace(ctx.Args[0])
	if domain == "" {
		return fmt.Errorf("allow requires <domain>")
	}
	added1, err1 := fz.AppendLineIfMissing(filepath.Join(ctx.Paths.Kit, "proxy", "allowlist.txt"), domain)
	dnsRule := fmt.Sprintf("server=/%s/1.1.1.1", domain)
	added2, err2 := fz.AppendLineIfMissing(filepath.Join(ctx.Paths.Kit, "dns", "dnsmasq.conf"), dnsRule)
	if err1 != nil || err2 != nil {
		if err1 != nil {
			fmt.Fprintln(os.Stderr, "allowlist:", err1)
		}
		if err2 != nil {
			fmt.Fprintln(os.Stderr, "dnsmasq:", err2)
		}
		return fmt.Errorf("allow updates failed")
	}
	if added1 {
		fmt.Println("Added to proxy allowlist:", domain)
	} else {
		fmt.Println("Already in proxy allowlist:", domain)
	}
	if added2 {
		fmt.Println("Added to DNS allowlist:", domain)
	} else {
		fmt.Println("Already in DNS allowlist:", domain)
	}
	fmt.Printf("Note: restart dns and proxy to apply (devctl -p %s restart)\n", project)
	return nil
}
