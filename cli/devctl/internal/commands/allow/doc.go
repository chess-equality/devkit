// Package allow contains the add-domain helper for both proxy and DNS allowlists.
//
// Usage:
//
//	allow -p <project> example.com
//
// The command updates `kit/proxy/allowlist.txt` and `kit/dns/dnsmasq.conf`,
// reporting whether the domain was added. When the modularization is complete,
// `internal/commands` will host additional CLI command handlers.
package allow
