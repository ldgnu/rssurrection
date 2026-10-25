# syntax=docker/dockerfile:1

FROM golang:1.27-alpine AS build
WORKDIR /src
COPY go.mod go.sum* ./
RUN go mod download
COPY . .
ARG VERSION=dev
ARG COMMIT=none
RUN CGO_ENABLED=0 GOOS=linux go build \
      -trimpath \
      -ldflags="-s -w -X github.com/ldgnu/rssurrection/internal/buildinfo.Version=${VERSION} -X github.com/ldgnu/rssurrection/internal/buildinfo.Commit=${COMMIT}" \
      -o /out/rssurrection ./cmd/rssurrection

FROM alpine:3.22
RUN apk add --no-cache ca-certificates tzdata \
 && addgroup -S -g 10001 app \
 && adduser -S -D -H -u 10001 -G app app
WORKDIR /app
COPY --from=build /out/rssurrection /app/rssurrection
USER 10001:10001
EXPOSE 8080
HEALTHCHECK --interval=30s --timeout=3s --start-period=5s --retries=3 \
  CMD wget -q -O /dev/null http://127.0.0.1:8080/healthz || exit 1
ENTRYPOINT ["/app/rssurrection"]
