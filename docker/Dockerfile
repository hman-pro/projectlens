# ── Builder stage ─────────────────────────────────────────────
FROM golang:1.26-alpine AS builder

RUN apk add --no-cache git

WORKDIR /src

COPY go.mod go.sum ./
RUN go mod download

COPY . .

RUN CGO_ENABLED=0 go build -o /bin/lens       ./cmd/lens
RUN CGO_ENABLED=0 go build -o /bin/lens-mcp   ./cmd/lens-mcp

# ── Runtime stage ─────────────────────────────────────────────
FROM alpine:3.21

RUN apk add --no-cache ca-certificates git

COPY --from=builder /bin/lens      /bin/lens
COPY --from=builder /bin/lens-mcp  /bin/lens-mcp
COPY --from=builder /src/migrations/     /migrations/
COPY --from=builder /src/configs/        /configs/

ENTRYPOINT ["/bin/lens"]
