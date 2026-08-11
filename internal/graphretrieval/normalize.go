package graphretrieval

import (
	"strings"

	"github.com/Wrath-y/local-rag/internal/graphsnapshot"
)

func DecodeRequest(body []byte) (Request, *graphsnapshot.Error) {
	var request Request
	if err := graphsnapshot.DecodeStrictJSON(body, &request); err != nil {
		return Request{}, graphsnapshot.NewError(graphsnapshot.CodeInvalidRetrievalRequest, map[string]any{"field": "body"}, err)
	}
	return request, nil
}

func Normalize(request Request) (NormalizedRequest, *graphsnapshot.Error) {
	query := strings.TrimSpace(request.Query)
	if query == "" {
		return NormalizedRequest{}, invalid("query")
	}
	filter, graphErr := normalizeFilter(request)
	if graphErr != nil {
		return NormalizedRequest{}, graphErr
	}
	seedLimit, graphErr := normalizeLimit("seed_limit", request.SeedLimit, DefaultSeedLimit, MaxSeedLimit)
	if graphErr != nil {
		return NormalizedRequest{}, graphErr
	}
	resultLimit, graphErr := normalizeLimit("result_limit", request.ResultLimit, DefaultResultLimit, MaxResultLimit)
	if graphErr != nil {
		return NormalizedRequest{}, graphErr
	}
	depth, graphErr := normalizeDepth(request.GraphDepth)
	if graphErr != nil {
		return NormalizedRequest{}, graphErr
	}
	return NormalizedRequest{Query: query, Filter: filter, SeedLimit: seedLimit, ResultLimit: resultLimit, GraphDepth: depth}, nil
}

func normalizeFilter(request Request) (Filter, *graphsnapshot.Error) {
	if request.SnapshotVersion != "" && strings.TrimSpace(request.SnapshotVersion) == "" {
		return Filter{}, invalid("snapshot_version")
	}
	nodeTypes, graphErr := normalizedOptionalSet("node_types", request.NodeTypes)
	if graphErr != nil {
		return Filter{}, graphErr
	}
	edgeTypes, graphErr := normalizedOptionalSet("edge_types", request.EdgeTypes)
	if graphErr != nil {
		return Filter{}, graphErr
	}
	relationshipKinds := request.RelationshipKinds
	if relationshipKinds == nil {
		relationshipKinds = []string{"explicit"}
	}
	relationshipKinds, graphErr = normalizedRequiredSet("relationship_kinds", relationshipKinds)
	if graphErr != nil {
		return Filter{}, graphErr
	}
	for _, kind := range relationshipKinds {
		if kind != "explicit" && kind != "inferred" {
			return Filter{}, invalid("relationship_kinds")
		}
	}
	return Filter{SnapshotVersion: request.SnapshotVersion, NodeTypes: nodeTypes, EdgeTypes: edgeTypes, RelationshipKinds: relationshipKinds}, nil
}

func normalizeLimit(field string, value *int, fallback, maximum int) (int, *graphsnapshot.Error) {
	if value == nil {
		return fallback, nil
	}
	if *value > maximum {
		return 0, graphsnapshot.NewError(graphsnapshot.CodeLimitExceeded, map[string]any{"field": field, "max": maximum}, nil)
	}
	if *value < 1 {
		return 0, invalid(field)
	}
	return *value, nil
}

func normalizeDepth(value *int) (int, *graphsnapshot.Error) {
	if value == nil {
		return DefaultGraphDepth, nil
	}
	if *value > MaxGraphDepth {
		return 0, graphsnapshot.NewError(graphsnapshot.CodeLimitExceeded, map[string]any{"field": "graph_depth", "max": MaxGraphDepth}, nil)
	}
	if *value < 0 {
		return 0, invalid("graph_depth")
	}
	return *value, nil
}

func normalizedOptionalSet(field string, values []string) ([]string, *graphsnapshot.Error) {
	if values == nil {
		return nil, nil
	}
	return normalizedRequiredSet(field, values)
}

func normalizedRequiredSet(field string, values []string) ([]string, *graphsnapshot.Error) {
	if len(values) == 0 {
		return nil, invalid(field)
	}
	seen := make(map[string]struct{}, len(values))
	result := make([]string, 0, len(values))
	for _, value := range values {
		if strings.TrimSpace(value) == "" || value != strings.TrimSpace(value) {
			return nil, invalid(field)
		}
		if _, duplicate := seen[value]; duplicate {
			return nil, invalid(field)
		}
		seen[value] = struct{}{}
		result = append(result, value)
	}
	// Byte-ordering supplies a canonical fingerprint without changing case.
	sortStrings(result)
	return result, nil
}

func sortStrings(values []string) {
	for i := 1; i < len(values); i++ {
		for j := i; j > 0 && values[j] < values[j-1]; j-- {
			values[j], values[j-1] = values[j-1], values[j]
		}
	}
}

func invalid(field string) *graphsnapshot.Error {
	return graphsnapshot.NewError(graphsnapshot.CodeInvalidRetrievalRequest, map[string]any{"field": field}, nil)
}
