# syntax=docker/dockerfile:1

# 前端构建
FROM node:24-alpine AS web
WORKDIR /build/web
COPY web/package.json web/pnpm-lock.yaml ./
RUN corepack enable && corepack prepare pnpm@9.15.9 --activate && pnpm install --frozen-lockfile
COPY web/ ./
RUN pnpm build

# 后端构建
FROM golang:1.26-alpine AS go
ENV GOPROXY=https://goproxy.cn,direct
WORKDIR /build
COPY go.mod go.sum ./
RUN go mod download
COPY cmd/ cmd/
COPY internal/ internal/
COPY web/embed.go web/embed.go
COPY --from=web /build/web/dist web/dist
ARG version=dev
RUN CGO_ENABLED=0 go build -ldflags "-s -w -X main.version=${version}" -o /wispjar ./cmd/wispjar

# 运行时
FROM alpine:3
RUN apk add --no-cache ca-certificates
COPY --from=go /wispjar /usr/local/bin/wispjar
RUN chmod +x /usr/local/bin/wispjar
ENTRYPOINT ["wispjar"]
