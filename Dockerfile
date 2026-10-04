# syntax=docker/dockerfile:1.7
FROM --platform=$BUILDPLATFORM node:22-alpine AS web
WORKDIR /src/web
COPY web/package.json web/package-lock.json ./
RUN npm ci
COPY web/ ./
RUN npm run build

FROM --platform=$BUILDPLATFORM golang:1.27-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
COPY --from=web /src/web/build/ /src/internal/webui/dist/
ARG VERSION
ARG TARGETOS
ARG TARGETARCH
RUN build_version="${VERSION:-$(tr -d '\r\n' < VERSION)}" && \
    CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH go build -trimpath -ldflags="-s -w -X main.version=${build_version}" -o /out/rivetpanel ./cmd/rivetpanel && \
    CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH go build -trimpath -ldflags="-s -w -X main.version=${build_version}" -o /out/rivet-agent ./cmd/rivet-agent

FROM alpine:3.22
RUN apk add --no-cache ca-certificates tzdata
COPY --from=build /out/rivetpanel /usr/local/bin/rivetpanel
COPY --from=build /out/rivet-agent /usr/local/bin/rivet-agent
COPY runtimes /opt/rivetpanel/runtimes
WORKDIR /var/lib/rivetpanel
ENV RIVET_ENV=production \
    RIVET_LISTEN=0.0.0.0:8080 \
    RIVET_DB_PATH=/var/lib/rivetpanel/rivetpanel.db \
    RIVET_DATA_ROOT=/var/lib/rivetpanel/workspaces \
    RIVET_KEY_DIR=/var/lib/rivetpanel/keys \
    RIVET_RUNTIMES_DIR=/opt/rivetpanel/runtimes
VOLUME ["/var/lib/rivetpanel"]
EXPOSE 8080 2022 8081 8444
ENTRYPOINT ["rivetpanel"]
