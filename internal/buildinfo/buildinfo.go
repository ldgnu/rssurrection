// Package buildinfo exposes build metadata injected at link time.
package buildinfo

// Overridden via -ldflags "-X github.com/ldgnu/rssurrection/internal/buildinfo.Version=..."
var (
	Version = "dev"
	Commit  = "none"
	Date    = "unknown"
)
