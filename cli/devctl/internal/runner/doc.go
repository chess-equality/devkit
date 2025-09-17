// Package runner centralizes helpers that execute host and docker-compose commands.
//
// These wrappers keep consistent timeout, dry-run logging, and exit-handling
// semantics across the CLI. They were previously embedded in main.go, but moving
// them here makes it easier to reuse the behavior from other packages as the
// CLI is decomposed.
package runner
