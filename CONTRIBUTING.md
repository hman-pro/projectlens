# Contributing to ProjectLens

ProjectLens is in public alpha. The codebase is still moving fast; non-trivial
changes are best discussed in a GitHub issue before opening a PR.

## Development setup

```bash
# Clone and bootstrap
git clone https://github.com/hman-pro/projectlens.git
cd projectlens
cp .env.example .env

# Postgres
cd docker && docker compose up -d
cd ..

# Build all binaries
make build
```

## Test commands

```bash
go fmt ./...
go vet ./...
go test ./...
make build
```

Integration tests require a running Postgres and are gated by the
`integration` build tag:

```bash
go test -tags integration ./...
```

## Style

- Go 1.26+, run `go fmt` before committing.
- Keep error wrapping with `fmt.Errorf("context: %w", err)`.
- Internal packages only — no public Go API surface.
