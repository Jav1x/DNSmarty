# Base images are pinned by digest; Dependabot bumps them. The tag is kept for readability.
FROM node:25-bookworm@sha256:78839ac448c23517f8eab2e8f7943d9b4f73979eb7f8bed2c73dbf72ff869e7b AS web
WORKDIR /web
COPY web/package.json web/package-lock.json ./
RUN npm ci --no-audit --no-fund
COPY web/ ./
RUN npm run build

FROM golang:1.26-bookworm@sha256:dc9ad6c05acc7a88e5b71bde60a5fe3bd4b9f0db209011711b464107438a8107 AS build
ARG VERSION=dev
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
COPY --from=web /internal/panel/dist ./internal/panel/dist
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath \
    -ldflags="-s -w -X dnsmarty/internal/buildinfo.Version=${VERSION}" \
    -o /out/dnsmarty ./cmd/dnsmarty

# distroless/static ships CA certificates and tzdata; no shell, no package manager.
FROM gcr.io/distroless/static-debian12:nonroot@sha256:afa5c872c891853ca7fcf1f12c3edb23f7eeef36189728842dd51042ff57f7ab
ARG VERSION=dev
LABEL org.opencontainers.image.title="dnsmarty" \
      org.opencontainers.image.source="https://github.com/Jav1x/DNSmarty" \
      org.opencontainers.image.version="${VERSION}"
COPY --from=build /out/dnsmarty /dnsmarty
USER nonroot:nonroot
ENTRYPOINT ["/dnsmarty"]
