# Base images are pinned by digest; Dependabot bumps them. The tag is kept for readability.
FROM node:22-bookworm@sha256:0e5f906573693feaa1e21057ebdcfdb5bd5021f050b2dc7c9deceb629c7da2a8 AS web
WORKDIR /web
COPY web/package.json web/package-lock.json ./
RUN npm ci --no-audit --no-fund
COPY web/ ./
RUN npm run build

FROM golang:1.26.9-bookworm@sha256:d9c68c2c51161e12fd77e4c6320687c9cd86e1af1e3ad6e6cd63ff970641453c AS build
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
