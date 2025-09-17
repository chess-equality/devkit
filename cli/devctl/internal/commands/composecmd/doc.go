// Package composecmd covers simple docker-compose lifecycle commands such as
// up/down/restart/status/logs.
//
// Handlers are registered with the CLI command registry so `main.go` stays
// focused on argument parsing.
package composecmd
