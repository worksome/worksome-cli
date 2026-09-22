// Package buildinfo holds the build stamps shared by every binary in this
// module, so the CLI and the introspection tool report the same version rather
// than each carrying its own copy.
package buildinfo

// Version and Commit are set at link time by the Makefile and GoReleaser with
// -X github.com/worksome/worksome-cli/internal/buildinfo.<name>=<value>. They
// must stay plain package-level string vars: -X cannot set a const, and it is
// silently ignored when the symbol it names does not exist, so a rename here
// without the matching change in the Makefile and .goreleaser.yml leaves every
// binary reporting the defaults below.
var (
	Version = "dev"
	Commit  = "none"
)
