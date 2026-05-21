package export

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"time"

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

// Export writes a complete graph JSON envelope to w.
func (g *GraphExporter) Export(ctx context.Context, w io.Writer, opts Options) error {
	edgeTypes := opts.resolveEdges()

	gs := indexstate.GitState{}
	if g.inspector != nil {
		gs = g.inspector.GitHeadAndDirty(ctx)
	}

	fmt.Fprintf(w, `{"schema_version":%q,"generated_at":%q,"git_head":%q,"git_dirty":%t,"nodes":[`,
		SchemaVersion,
		time.Now().UTC().Format(time.RFC3339),
		gs.Head,
		gs.Dirty)

	emittedNodes := map[string]struct{}{}
	first := true
	emit := func(id string, jsonBytes []byte) error {
		if !first {
			if _, err := w.Write([]byte(",")); err != nil {
				return err
			}
		}
		first = false
		if id != "" {
			emittedNodes[id] = struct{}{}
		}
		_, err := w.Write(jsonBytes)
		return err
	}

	if err := streamSymbols(ctx, g.db, emit); err != nil {
		return err
	}
	if err := streamFiles(ctx, g.db, emit); err != nil {
		return err
	}
	if err := streamTables(ctx, g.db, emit); err != nil {
		return err
	}
	if err := streamPackages(ctx, g.db, emit); err != nil {
		return err
	}
	if err := streamKnowledge(ctx, g.db, emit); err != nil {
		return err
	}

	fmt.Fprintf(w, `],"edges":[`)
	first = true
	emitEdge := func(jsonBytes []byte) error {
		if !first {
			if _, err := w.Write([]byte(",")); err != nil {
				return err
			}
		}
		first = false
		_, err := w.Write(jsonBytes)
		return err
	}
	if err := streamEdges(ctx, g.db, edgeTypes, opts.IncludeEvidence, emittedNodes, emitEdge); err != nil {
		return err
	}
	fmt.Fprintf(w, `]}`)
	return nil
}

type nodeOut struct {
	ID    string                 `json:"id"`
	Type  string                 `json:"type"`
	Label string                 `json:"label"`
	Attrs map[string]interface{} `json:"attrs,omitempty"`
}

type edgeOut struct {
	Source     string                 `json:"source"`
	Target     string                 `json:"target"`
	Type       string                 `json:"type"`
	Confidence *float64               `json:"confidence,omitempty"`
	SourceAttr string                 `json:"source_attr,omitempty"`
	Properties map[string]interface{} `json:"properties,omitempty"`
}

func streamSymbols(ctx context.Context, db *storage.DB, emit func(string, []byte) error) error {
	rows, err := db.Pool.Query(ctx,
		`SELECT id, package_name, name, kind, file_id FROM symbols ORDER BY id`)
	if err != nil {
		return fmt.Errorf("export: symbols: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var id, fileID int64
		var pkg, name, kind string
		if err := rows.Scan(&id, &pkg, &name, &kind, &fileID); err != nil {
			return fmt.Errorf("export: symbols scan: %w", err)
		}
		n := nodeOut{
			ID:    nodeID(kindSymbol, id, "", "", "", ""),
			Type:  "symbol",
			Label: name,
			Attrs: map[string]interface{}{"package": pkg, "kind": kind, "file_id": fileID},
		}
		b, err := json.Marshal(n)
		if err != nil {
			return err
		}
		if err := emit(n.ID, b); err != nil {
			return err
		}
	}
	return rows.Err()
}

func streamFiles(ctx context.Context, db *storage.DB, emit func(string, []byte) error) error {
	rows, err := db.Pool.Query(ctx, `SELECT id, path, package_name FROM files ORDER BY id`)
	if err != nil {
		return fmt.Errorf("export: files: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var id int64
		var path string
		var pkg *string
		if err := rows.Scan(&id, &path, &pkg); err != nil {
			return fmt.Errorf("export: files scan: %w", err)
		}
		attrs := map[string]interface{}{"path": path}
		if pkg != nil {
			attrs["package"] = *pkg
		}
		n := nodeOut{
			ID:    nodeID(kindFile, id, "", "", "", ""),
			Type:  "file",
			Label: path,
			Attrs: attrs,
		}
		b, err := json.Marshal(n)
		if err != nil {
			return err
		}
		if err := emit(n.ID, b); err != nil {
			return err
		}
	}
	return rows.Err()
}

func streamTables(ctx context.Context, db *storage.DB, emit func(string, []byte) error) error {
	rows, err := db.Pool.Query(ctx, `SELECT id, engine, schema_name, name FROM datastore_tables ORDER BY id`)
	if err != nil {
		return fmt.Errorf("export: tables: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var id int64
		var engine, name string
		var schema *string
		if err := rows.Scan(&id, &engine, &schema, &name); err != nil {
			return fmt.Errorf("export: tables scan: %w", err)
		}
		schemaStr := ""
		if schema != nil {
			schemaStr = *schema
		}
		label := name
		if schemaStr != "" {
			label = schemaStr + "." + name
		}
		n := nodeOut{
			ID:    nodeID(kindDatastoreTable, id, engine, schemaStr, name, ""),
			Type:  "datastore_table",
			Label: label,
			Attrs: map[string]interface{}{"engine": engine, "schema": schemaStr},
		}
		b, err := json.Marshal(n)
		if err != nil {
			return err
		}
		if err := emit(n.ID, b); err != nil {
			return err
		}
	}
	return rows.Err()
}

func streamPackages(ctx context.Context, db *storage.DB, emit func(string, []byte) error) error {
	rows, err := db.Pool.Query(ctx, `
		SELECT DISTINCT package_name FROM symbols
		UNION
		SELECT DISTINCT package_name FROM files WHERE package_name IS NOT NULL
		UNION
		SELECT DISTINCT package_name FROM summaries
	`)
	if err != nil {
		return fmt.Errorf("export: packages: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var pkg string
		if err := rows.Scan(&pkg); err != nil {
			return fmt.Errorf("export: packages scan: %w", err)
		}
		n := nodeOut{
			ID:    nodeID(kindPackage, 0, "", "", "", pkg),
			Type:  "package",
			Label: pkg,
		}
		b, err := json.Marshal(n)
		if err != nil {
			return err
		}
		if err := emit(n.ID, b); err != nil {
			return err
		}
	}
	return rows.Err()
}

func streamKnowledge(ctx context.Context, db *storage.DB, emit func(string, []byte) error) error {
	rows, err := db.Pool.Query(ctx, `SELECT id, title, category FROM knowledge_entries ORDER BY id`)
	if err != nil {
		return fmt.Errorf("export: knowledge: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var id int64
		var title, category string
		if err := rows.Scan(&id, &title, &category); err != nil {
			return fmt.Errorf("export: knowledge scan: %w", err)
		}
		n := nodeOut{
			ID:    nodeID(kindKnowledge, id, "", "", "", ""),
			Type:  "knowledge",
			Label: title,
			Attrs: map[string]interface{}{"category": category},
		}
		b, err := json.Marshal(n)
		if err != nil {
			return err
		}
		if err := emit(n.ID, b); err != nil {
			return err
		}
	}
	return rows.Err()
}

func streamEdges(ctx context.Context, db *storage.DB, edgeTypes []string, includeEvidence bool, emittedNodes map[string]struct{}, emit func([]byte) error) error {
	rows, err := db.Pool.Query(ctx, `
		SELECT e.source_type, e.source_id, e.target_type, e.target_id,
		       e.edge_type, e.confidence, e.properties,
		       dt_src.engine, dt_src.schema_name, dt_src.name,
		       dt_tgt.engine, dt_tgt.schema_name, dt_tgt.name,
		       f_src.package_name, f_tgt.package_name
		FROM edges e
		LEFT JOIN datastore_tables dt_src
		  ON e.source_type = 'datastore_table' AND dt_src.id = e.source_id
		LEFT JOIN datastore_tables dt_tgt
		  ON e.target_type = 'datastore_table' AND dt_tgt.id = e.target_id
		LEFT JOIN files f_src
		  ON e.source_type = 'package' AND f_src.id = e.source_id
		LEFT JOIN files f_tgt
		  ON e.target_type = 'package' AND f_tgt.id = e.target_id
		WHERE e.edge_type = ANY($1)
		ORDER BY e.id
	`, edgeTypes)
	if err != nil {
		return fmt.Errorf("export: edges: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var srcType, tgtType, etype string
		var srcID, tgtID int64
		var conf *float64
		var props map[string]interface{}
		var srcEngine, srcSchema, srcName *string
		var tgtEngine, tgtSchema, tgtName *string
		var srcPkg, tgtPkg *string
		if err := rows.Scan(
			&srcType, &srcID, &tgtType, &tgtID,
			&etype, &conf, &props,
			&srcEngine, &srcSchema, &srcName,
			&tgtEngine, &tgtSchema, &tgtName,
			&srcPkg, &tgtPkg,
		); err != nil {
			return fmt.Errorf("export: edges scan: %w", err)
		}

		sourceID := edgeEndpoint(srcType, srcID, srcEngine, srcSchema, srcName, srcPkg, props, true)
		targetID := edgeEndpoint(tgtType, tgtID, tgtEngine, tgtSchema, tgtName, tgtPkg, props, false)

		if _, ok := emittedNodes[sourceID]; !ok {
			continue
		}
		if _, ok := emittedNodes[targetID]; !ok {
			continue
		}

		if !includeEvidence && props != nil {
			delete(props, "evidence")
		}
		sourceAttr := ""
		if props != nil {
			if v, ok := props["source_attr"].(string); ok {
				sourceAttr = v
			}
		}
		if sourceAttr == "" {
			sourceAttr = "unknown"
		}

		e := edgeOut{
			Source:     sourceID,
			Target:     targetID,
			Type:       etype,
			Confidence: conf,
			SourceAttr: sourceAttr,
			Properties: props,
		}
		b, err := json.Marshal(e)
		if err != nil {
			return err
		}
		if err := emit(b); err != nil {
			return err
		}
	}
	return rows.Err()
}

func edgeEndpoint(t string, id int64, engine, schema, name, pkgFromFile *string, props map[string]interface{}, isSource bool) string {
	switch t {
	case "symbol":
		return nodeID(kindSymbol, id, "", "", "", "")
	case "file":
		return nodeID(kindFile, id, "", "", "", "")
	case "datastore_table":
		eng := ""
		sch := ""
		nm := ""
		if engine != nil {
			eng = *engine
		}
		if schema != nil {
			sch = *schema
		}
		if name != nil {
			nm = *name
		}
		return nodeID(kindDatastoreTable, id, eng, sch, nm, "")
	case "knowledge":
		return nodeID(kindKnowledge, id, "", "", "", "")
	case "package":
		// InsertKnowledgeAnchors stores target_type='package' with target_id =
		// representative files.id. Resolve via the JOINed files.package_name
		// first; fall back to source_package / target_package property only
		// if the file row vanished.
		pkg := ""
		if pkgFromFile != nil {
			pkg = *pkgFromFile
		}
		if pkg == "" && props != nil {
			key := "target_package"
			if isSource {
				key = "source_package"
			}
			if v, ok := props[key].(string); ok {
				pkg = v
			}
		}
		return nodeID(kindPackage, 0, "", "", "", pkg)
	default:
		return fmt.Sprintf("unknown:%s:%d", t, id)
	}
}
