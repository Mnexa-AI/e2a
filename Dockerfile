FROM golang:1.26-alpine AS builder

WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -o /e2a ./cmd/e2a

FROM alpine:3.22
RUN apk add --no-cache ca-certificates wget \
    && adduser -D -u 1001 -h /home/e2a e2a
COPY --from=builder /e2a /usr/local/bin/e2a
COPY config.example.yaml /etc/e2a/config.yaml
USER e2a
EXPOSE 2525 8080
HEALTHCHECK --interval=10s --timeout=3s --start-period=10s --retries=5 \
    CMD wget -qO- http://127.0.0.1:8080/api/health || exit 1
ENTRYPOINT ["e2a", "-config", "/etc/e2a/config.yaml"]
