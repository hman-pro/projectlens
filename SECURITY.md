# Security Policy

## Data Boundary

ProjectLens is local-first. By default:

- Source repositories are read locally and never uploaded.
- All vector storage and metadata live in your local Postgres.
- Embeddings and (optional) summaries run against a local Ollama endpoint.
- Generated reports and graph exports become egress surfaces only if you share them.

The public alpha does not include any remote provider integrations.

## Reporting a vulnerability

If you discover a security issue, please report it via GitHub private
vulnerability reporting on the repository's Security tab.

Please include reproduction steps and the affected version or commit. We aim
to acknowledge reports within 72 hours and to fix high-severity issues within
14 days.
