# ── Builder stage ─────────────────────────────────────────────
FROM golang:1.26-alpine AS builder

RUN apk add --no-cache git

WORKDIR /src

COPY go.mod go.sum ./
RUN go mod download

COPY . .

RUN CGO_ENABLED=0 go build -o /bin/projectlens       ./cmd/projectlens
RUN CGO_ENABLED=0 go build -o /bin/projectlens-mcp   ./cmd/projectlens-mcp

# ── Runtime stage ─────────────────────────────────────────────
FROM alpine:3.21

RUN apk add --no-cache ca-certificates

COPY --from=builder /bin/projectlens      /bin/projectlens
COPY --from=builder /bin/projectlens-mcp  /bin/projectlens-mcp
COPY --from=builder /src/migrations/     /migrations/
COPY --from=builder /src/configs/        /configs/

ENTRYPOINT ["/bin/projectlens"]
