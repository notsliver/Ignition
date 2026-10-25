// Package version describes the running build. The values are set at build time with -ldflags -X
// (the installer passes the git commit it built from).
package version

var (
	Version = "dev"     // e.g. 0.12.0, or "dev" for untagged builds
	Commit  = "unknown" // full git SHA
	BuiltAt = ""        // RFC 3339
	Dirty   = "false"   // "true" when built with uncommitted changes
)
