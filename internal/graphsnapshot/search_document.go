package graphsnapshot

import (
	"fmt"

	"github.com/gowebpki/jcs"
)

const SearchDocumentFormatV1 = "graph-search-v1"

// SearchDocument returns a stable, explicit text projection. It is a future
// FTS/vector input, never a source of graph facts; JSON fields are JCS bytes
// so property member order cannot change search document identities.
func SearchDocument(node *Node, edge *Edge) (string, error) {
	if node != nil {
		properties, err := jcs.Transform(node.Properties)
		if err != nil {
			return "", err
		}
		return fmt.Sprintf("%s\nkind=node\nid=%s\ntype=%s\nlabel=%s\ntext=%s\nproperties=%s", SearchDocumentFormatV1, node.ID, node.Type, node.Label, node.Text, properties), nil
	}
	if edge != nil {
		properties, err := jcs.Transform(edge.Properties)
		if err != nil {
			return "", err
		}
		return fmt.Sprintf("%s\nkind=edge\nid=%s\nfrom=%s\nto=%s\ntype=%s\nrelation_kind=%s\nproperties=%s", SearchDocumentFormatV1, edge.ID, edge.From, edge.To, edge.Type, edge.RelationKind, properties), nil
	}
	return "", fmt.Errorf("graph search document requires a node or edge")
}
