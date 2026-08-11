package graphretrieval

import (
	"testing"

	"github.com/Wrath-y/local-rag/internal/graphsnapshot"
)

func pointer(value int) *int { return &value }

func TestNormalizeAppliesStrictDefaultsAndCanonicalFilters(t *testing.T) {
	normalized, graphErr := Normalize(Request{Query: "  find this  ", NodeTypes: []string{"z", "A"}})
	if graphErr != nil {
		t.Fatal(graphErr)
	}
	if normalized.Query != "find this" || normalized.SeedLimit != 20 || normalized.ResultLimit != 20 || normalized.GraphDepth != 1 {
		t.Fatalf("normalized=%+v", normalized)
	}
	if got := normalized.NodeTypes; len(got) != 2 || got[0] != "A" || got[1] != "z" {
		t.Fatalf("node types=%v", got)
	}
	if got := normalized.RelationshipKinds; len(got) != 1 || got[0] != "explicit" {
		t.Fatalf("relationship default=%v", got)
	}
}

func TestNormalizeRejectsInvalidAndBoundedInputsBeforeWork(t *testing.T) {
	cases := []struct {
		name    string
		request Request
		code    graphsnapshot.Code
	}{
		{"blank query", Request{Query: " \t"}, graphsnapshot.CodeInvalidRetrievalRequest},
		{"duplicate filter", Request{Query: "q", NodeTypes: []string{"type", "type"}}, graphsnapshot.CodeInvalidRetrievalRequest},
		{"empty explicit filter", Request{Query: "q", EdgeTypes: []string{}}, graphsnapshot.CodeInvalidRetrievalRequest},
		{"unknown relationship", Request{Query: "q", RelationshipKinds: []string{"other"}}, graphsnapshot.CodeInvalidRetrievalRequest},
		{"zero limit", Request{Query: "q", SeedLimit: pointer(0)}, graphsnapshot.CodeInvalidRetrievalRequest},
		{"seed maximum", Request{Query: "q", SeedLimit: pointer(101)}, graphsnapshot.CodeLimitExceeded},
		{"depth maximum", Request{Query: "q", GraphDepth: pointer(4)}, graphsnapshot.CodeLimitExceeded},
		{"negative depth", Request{Query: "q", GraphDepth: pointer(-1)}, graphsnapshot.CodeInvalidRetrievalRequest},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			_, graphErr := Normalize(testCase.request)
			if graphErr == nil || graphErr.Code != testCase.code {
				t.Fatalf("error=%v, want %s", graphErr, testCase.code)
			}
		})
	}
}

func TestDecodeRequestRejectsUnknownAndDuplicateMembers(t *testing.T) {
	for _, body := range []string{
		`{"query":"q","unknown":true}`,
		`{"query":"q","query":"again"}`,
		`{"query":"q"} trailing`,
	} {
		if _, graphErr := DecodeRequest([]byte(body)); graphErr == nil || graphErr.Code != graphsnapshot.CodeInvalidRetrievalRequest {
			t.Fatalf("body %s error=%v", body, graphErr)
		}
	}
}

func TestWarningsAreStableAndExposeOnlyRetryability(t *testing.T) {
	warnings := []Warning{}
	for _, item := range []struct {
		stage   Stage
		outcome StageOutcome
	}{{StageRerank, StageTransientFailure}, {StageBM25, StageUnavailable}, {StageVector, StageTransientFailure}} {
		warning, ok := WarningFor(item.stage, item.outcome)
		if !ok {
			t.Fatalf("missing warning for %s/%s", item.stage, item.outcome)
		}
		warnings = append(warnings, warning)
	}
	SortWarnings(warnings)
	if warnings[0].Stage != StageBM25 || warnings[1].Stage != StageVector || warnings[2].Stage != StageRerank {
		t.Fatalf("warning order=%+v", warnings)
	}
	if !warnings[1].Retryable || !warnings[2].Retryable || warnings[0].Retryable {
		t.Fatalf("warning retryability=%+v", warnings)
	}
}

func TestBaseFailureRetryabilityRequiresOnlyTransientFailures(t *testing.T) {
	if !BaseFailureRetryable(StageTransientFailure, StageTransientFailure) {
		t.Fatal("all transient base failures should be retryable")
	}
	if BaseFailureRetryable(StageTransientFailure, StagePermanentFailure) || BaseFailureRetryable(StageUnavailable, StageUnavailable) {
		t.Fatal("permanent or unavailable base stages must not be retryable")
	}
}
