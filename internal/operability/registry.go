package operability

import (
	"sort"

	"github.com/Wrath-y/local-rag/internal/buildinfo"
)

// Registry owns the deterministic, transport-neutral compatibility surface.
// Its inputs are already safe diagnostic DTOs, never raw dependency errors.
type Registry struct {
	Status       HealthStatus
	Capabilities []Capability
	Limits       []Limit
	Dependencies []Dependency
}

func (r Registry) Document() Health {
	capabilities := append([]Capability(nil), r.Capabilities...)
	limits := append([]Limit(nil), r.Limits...)
	dependencies := append([]Dependency(nil), r.Dependencies...)
	sort.Slice(capabilities, func(i, j int) bool { return capabilities[i].Name < capabilities[j].Name })
	sort.Slice(limits, func(i, j int) bool { return limits[i].Name < limits[j].Name })
	sort.Slice(dependencies, func(i, j int) bool { return dependencies[i].Name < dependencies[j].Name })
	return Health{SchemaVersion: buildinfo.HealthSchemaVersion, Status: r.Status, Service: buildinfo.ServiceName, ServiceVersion: buildinfo.ServiceVersion(), APIVersions: []string{buildinfo.GraphAPIVersion}, SupportedSchemaVersions: []string{buildinfo.GraphSchemaVersion}, Capabilities: capabilities, Limits: limits, Dependencies: dependencies}
}
