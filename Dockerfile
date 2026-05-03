# ── Build stage ──────────────────────────────────────────────────────────────
FROM golang:1.23-alpine AS builder

WORKDIR /src

COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 go build -o /srtrelay ./cmd/srtrelay

# ── Runtime stage ─────────────────────────────────────────────────────────────
FROM alpine:3.21

RUN adduser -D -H -u 1000 srtrelay
USER srtrelay

COPY --from=builder /srtrelay /usr/local/bin/srtrelay

ENTRYPOINT ["/usr/local/bin/srtrelay"]
