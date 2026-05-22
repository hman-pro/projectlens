# Review: Confidence and Provenance Design

Reviewed: 2026-05-22
Target: `docs/2026-05-22-confidence-and-provenance-design.md`
Pass: third review after second-review fixes shipped

## Verdict

The two second-review findings are addressed in the current checkout:

- `internal/report` tests now seed `EdgeTrust`, assert the markdown Edge Trust section and rows, and assert JSON round-trip preservation.
- `internal/export` now has a focused provenance integration test that decodes top-level `provenance` and `confidence_class` on exported edges.
- The export surface prose now correctly says edge trust fields are top-level edge fields, not nested under edge `attrs`.

The original high/medium blockers from the first pass also remain addressed:

- MCP trust fields are present on `SymbolHit`, `TableEdgeHit`, and `CouplingEntry`, with top-level `Trust` on the three pilot payloads.
- `BackfillProvenance` repairs either missing column and preserves existing values.
- `edges_provenance_check` constrains provenance to the documented vocabulary.
- The spec lists the actual writer call sites and actual CLI/migration names.
- `--show-ambiguous-only` is no longer presented as in-scope for this phase.

## Findings

### 1. Export mapping test does not guarantee all seven mappings are exercised

Severity: low

`TestExportGraph_EdgeProvenance` defines the full known type-to-provenance map (`internal/export/graph_integration_test.go:131-140`), but the test only checks exported edges that are present in the live DB (`internal/export/graph_integration_test.go:141-149`). It does not track which expected edge types were seen.

That means the test can pass while some mappings are not exercised. In this local index, the live edge-type count has rows for `calls`, `co_changes`, `implements`, `imports`, and `knowledge_about`, but no `reads_table` or `writes_table` rows. The test still passes, so the sql-scanner export mapping is not actually covered here.

This is not a runtime blocker: every exported edge that exists is checked for non-empty provenance/class, and present known types are validated. It is only a coverage/wording gap if the intended claim is "spot-check all 7 type-to-provenance mappings."

Recommendation: either seed a small export fixture that contains all seven edge types, or track `seen` expected types and skip/fail explicitly when the live DB is too sparse. If live-data-dependent coverage is acceptable, soften the spec wording to say the test checks known mappings for edge types present in the current index.

## Verification Run

- `go test -count=1 ./internal/report ./internal/export ./internal/mcpserver ./internal/storage` passed.
- `DATABASE_URL='postgres://projectlens:projectlens@localhost:5433/projectlens?sslmode=disable' go test -tags integration -count=1 ./internal/storage ./internal/mcpserver ./internal/export -run 'Test(InsertEdgesProvenance|BackfillProvenance_PartialRepair|Integration_GetSymbolContext_ProvenanceAndTrust|Integration_GetTableContext_TrustAndProvenance|Integration_GetCoupling_TrustAndProvenance|ExportGraph_ClosureInvariant|ExportGraph_EdgeProvenance)' -v` passed for storage, symbol-context, coupling, and export provenance/closure tests; table-context still skipped because the current index has no reads/writes_table candidate.
- `SELECT COUNT(*) FROM edges WHERE provenance IS NULL OR confidence_class IS NULL` returned `0`.
- Live edge-type counts: `calls`, `co_changes`, `implements`, `imports`, and `knowledge_about` are present; `reads_table` and `writes_table` are absent in this index.

## Notes

ProjectLens MCP was used for orientation, but its index was stale for this dirty checkout, so this review is based on direct filesystem, test, and local DB verification.
