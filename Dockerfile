# syntax=docker/dockerfile:1

FROM golang:1.26-alpine AS builder
WORKDIR /src

# Install certificates for downloading modules behind TLS proxies.
RUN apk add --no-cache ca-certificates

COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o /out/goprint ./main.go

# Using scratch image for minimized size
FROM scratch
WORKDIR /app

COPY --from=builder /out/goprint /app/goprint
COPY --from=builder /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/ca-certificates.crt
COPY public ./public

EXPOSE 5001

ENTRYPOINT ["/app/goprint"]
CMD ["/app/config.yaml"]
