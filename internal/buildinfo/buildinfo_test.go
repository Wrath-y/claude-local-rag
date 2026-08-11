package buildinfo

import "testing"

func TestServiceVersionUsesSemVerLinkerValueOrFallback(t *testing.T) {
	original := Version
	t.Cleanup(func() { Version = original })
	for _, testCase := range []struct {
		value string
		want  string
	}{
		{"1.2.3", "1.2.3"},
		{"1.2.3-rc.1+build.7", "1.2.3-rc.1+build.7"},
		{"not-a-version", defaultVersion},
	} {
		Version = testCase.value
		if got := ServiceVersion(); got != testCase.want || !IsSemVer(got) {
			t.Fatalf("ServiceVersion(%q)=%q, want %q", testCase.value, got, testCase.want)
		}
	}
}
