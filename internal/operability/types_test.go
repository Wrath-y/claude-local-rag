package operability

import "testing"

func TestNormalizeComponentsAndFingerprintAreCanonical(t *testing.T) {
	first, err := NormalizeComponents([]Component{ComponentVector, ComponentGraphIndexes, ComponentFTS})
	if err != nil {
		t.Fatal(err)
	}
	second, err := NormalizeComponents([]Component{ComponentFTS, ComponentVector, ComponentGraphIndexes})
	if err != nil {
		t.Fatal(err)
	}
	if got, want := RequestFingerprint(first), RequestFingerprint(second); got != want || got == "" {
		t.Fatalf("fingerprints=%q,%q", got, want)
	}
	for _, components := range [][]Component{nil, {ComponentFTS, ComponentFTS}, {"unknown"}} {
		if _, err := NormalizeComponents(components); err == nil {
			t.Fatalf("NormalizeComponents(%v) accepted invalid input", components)
		}
	}
}

func TestValidateIdempotencyKey(t *testing.T) {
	for _, value := range []string{"rebuild-1", "client.request_123"} {
		if err := ValidateIdempotencyKey(value); err != nil {
			t.Fatalf("key %q: %v", value, err)
		}
	}
	for _, value := range []string{"", "bad key", "bad/key"} {
		if err := ValidateIdempotencyKey(value); err == nil {
			t.Fatalf("key %q accepted", value)
		}
	}
}
