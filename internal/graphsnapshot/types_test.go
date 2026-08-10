package graphsnapshot

import (
	"strings"
	"testing"
)

func TestDecodeRequestRejectsDuplicateMembersAtEveryDepth(t *testing.T) {
	_, err := DecodeRequest(strings.NewReader(`{"schema_version":"1.0","schema_version":"1.0"}`))
	if err == nil || !strings.Contains(err.Error(), "duplicate object member") {
		t.Fatalf("duplicate root error = %v", err)
	}
	_, err = DecodeRequest(strings.NewReader(`{"schema_version":"1.0","mode":"full","content_hash":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","nodes":[{"id":"node","type":"kind","label":"label","text":"text","properties":{"x":1,"x":2},"provenance":{}}],"edges":[]}`))
	if err == nil || !strings.Contains(err.Error(), "duplicate object member") {
		t.Fatalf("duplicate nested error = %v", err)
	}
}

func TestDecodeRequestPreservesRawNumbersAndRejectsUnknownRootFields(t *testing.T) {
	request, err := DecodeRequest(strings.NewReader(`{"schema_version":"1.0","mode":"full","content_hash":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","nodes":[{"id":"node","type":"kind","label":"label","text":"text","properties":{"number":1.2300},"provenance":{}}],"edges":[]}`))
	if err != nil {
		t.Fatal(err)
	}
	if got := string(request.Nodes[0].Properties); !strings.Contains(got, "1.2300") {
		t.Fatalf("properties lost numeric spelling: %s", got)
	}
	_, err = DecodeRequest(strings.NewReader(`{"schema_version":"1.0","mode":"full","content_hash":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","nodes":[],"edges":[],"unknown":true}`))
	if err == nil || !strings.Contains(err.Error(), "unknown field") {
		t.Fatalf("unknown field error = %v", err)
	}
}
