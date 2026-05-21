package export

import (
	"context"
	"fmt"
	"io"

	"github.com/hman-pro/projectlens/internal/indexstate"
	"github.com/hman-pro/projectlens/internal/storage"
)

const SchemaVersion = "projectlens-graph/v1"

// AllowedEdgeTypes is the canonical raw-edge_type vocabulary the
// exporter and the --edges flag both consult. Adding a new edge_type
// to the indexer requires extending this list in the same change.
var AllowedEdgeTypes = []string{
	"calls", "implements", "imports",
	"reads_table", "writes_table",
	"co_changes",
	"knowledge_about",
}

func IsValidEdgeType(t string) bool {
	if t == "all" {
		return true
	}
	for _, a := range AllowedEdgeTypes {
		if a == t {
			return true
		}
	}
	return false
}

type Options struct {
	Edges           []string // nil or {"all"} means all
	IncludeEvidence bool
}

func (o Options) resolveEdges() []string {
	if len(o.Edges) == 0 {
		return AllowedEdgeTypes
	}
	for _, e := range o.Edges {
		if e == "all" {
			return AllowedEdgeTypes
		}
	}
	return o.Edges
}

// nodeID resolves the canonical node identifier for an edge endpoint or
// a node row. attrs carries the type-specific data needed to build the
// id (engine + schema + name for datastore_table, package_name for
// package, otherwise the row id).
type nodeKind string

const (
	kindSymbol         nodeKind = "symbol"
	kindFile           nodeKind = "file"
	kindDatastoreTable nodeKind = "datastore_table"
	kindPackage        nodeKind = "package"
	kindKnowledge      nodeKind = "knowledge"
)

func nodeID(kind nodeKind, id int64, engine, schema, name, pkgName string) string {
	switch kind {
	case kindSymbol:
		return fmt.Sprintf("sym:%d", id)
	case kindFile:
		return fmt.Sprintf("file:%d", id)
	case kindDatastoreTable:
		return fmt.Sprintf("table:%s:%s.%s", engine, schema, name)
	case kindPackage:
		return "package:" + pkgName
	case kindKnowledge:
		return fmt.Sprintf("knowledge:%d", id)
	default:
		return ""
	}
}

// GraphExporter streams nodes + edges from Postgres directly to an
// io.Writer.
type GraphExporter struct {
	db        *storage.DB
	inspector indexstate.Inspector
}

func NewGraphExporter(db *storage.DB, insp indexstate.Inspector) *GraphExporter {
	return &GraphExporter{db: db, inspector: insp}
}

// Export will write a complete graph JSON envelope to w. Implementation
// in the next task.
func (g *GraphExporter) Export(ctx context.Context, w io.Writer, opts Options) error {
	return fmt.Errorf("export: not yet implemented")
}
