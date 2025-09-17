// Package cmdregistry defines a lightweight command registry used by the CLI
// entrypoint. It maps string command names to handler functions that accept a
// shared Context payload. This allows individual command implementations to
// live in separate packages while main.go stays focused on argument parsing.
package cmdregistry
