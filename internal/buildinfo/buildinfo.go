// Package buildinfo exposes diagnostic build identity without making it a
// compatibility gate. Release builds may inject Version with -ldflags -X.
package buildinfo

import "regexp"

const defaultVersion = "0.0.0-dev"

var Version = defaultVersion

var semver = regexp.MustCompile(`^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(?:-[0-9A-Za-z-]+(?:\.[0-9A-Za-z-]+)*)?(?:\+[0-9A-Za-z-]+(?:\.[0-9A-Za-z-]+)*)?$`)

// ServiceVersion returns a valid SemVer value even when a malformed linker
// value is supplied by a local build invocation.
func ServiceVersion() string {
	if semver.MatchString(Version) {
		return Version
	}
	return defaultVersion
}

func IsSemVer(value string) bool { return semver.MatchString(value) }

const (
	ServiceName          = "local-rag"
	GraphAPIVersion      = "v1"
	GraphSchemaVersion   = "1.0"
	HealthSchemaVersion  = "1.0"
)
