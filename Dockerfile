# syntax=docker/dockerfile:1

# ─── Build stage ─────────────────────────────────────────────────────────────
FROM golang:1.26-alpine AS builder

# gcc + musl-dev are required for CGO (mattn/go-sqlite3 + sqlite-vec)
# ca-certificates is copied to runtime for HTTPS calls to OpenAI / Deepseek
RUN apk add --no-cache gcc musl-dev ca-certificates tzdata

WORKDIR /build

# Dependency layer — cached unless go.mod / go.sum change
COPY src/go.mod src/go.sum ./
RUN go mod download

# Source
COPY src/ .

# Static binary:
#   CGO_ENABLED=1     — required for mattn/go-sqlite3 and sqlite-vec
#   -extldflags "-static" — link musl statically so the binary has zero runtime deps
#   -s -w             — strip symbol table and DWARF, reduces binary size
#   -trimpath         — remove local build paths from binary
RUN CGO_ENABLED=1 GOOS=linux go build \
    -tags sqlite_fts5 \
    -ldflags='-s -w -extldflags "-static"' \
    -trimpath \
    -o /memory-service \
    ./cmd/server

# ─── Runtime stage ───────────────────────────────────────────────────────────
FROM scratch

# CA certs for HTTPS (OpenAI + Deepseek API calls)
COPY --from=builder /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/

# Timezone data (used for valid_from / valid_until parsing)
COPY --from=builder /usr/share/zoneinfo /usr/share/zoneinfo

# The binary
COPY --from=builder /memory-service /memory-service

EXPOSE 8080

ENTRYPOINT ["/memory-service"]