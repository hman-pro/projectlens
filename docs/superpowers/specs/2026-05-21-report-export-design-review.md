# Review: Report and Graph Export Design

Reviewed: 2026-05-21

## Findings

No remaining findings in this pass.

## Verification Notes

- The JSON example now uses the declared node-ID scheme: `sym:<id>`, `table:<engine>:<schema>.<name>`, and `knowledge:<id>`.
- Every example edge endpoint is present in the example node list, so the sample satisfies the graph-closure invariant.
- The normative graph export text uses one node-ID function for both nodes and edge endpoints and includes a graph-closure test requirement.
- Prior review findings around provider state vocabulary, storage query schema, raw edge filters, provider inspector wiring, writer liveness, package nodes, source-file counting, and missing-stage actions remain addressed in the current spec.
