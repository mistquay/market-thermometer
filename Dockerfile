# syntax=docker/dockerfile:1.7
# 标准三阶段多架构构建（可在任意机器或 CI 独立执行 docker build）：
#   Stage 1: 构建 React + Vite 前端产物 -> internal/webui/dist
#   Stage 2: 嵌入前端产物，纯 Go 静态编译目标架构二进制
#   Stage 3: 最小化 Alpine 运行镜像（非 root + 证书 + 时区 + 健康检查）

FROM --platform=$BUILDPLATFORM node:24-alpine AS web-builder
WORKDIR /src/web
RUN corepack enable && corepack prepare pnpm@11 --activate
COPY web/package.json web/pnpm-lock.yaml ./
RUN pnpm install --frozen-lockfile
COPY web/ ./
RUN pnpm build

FROM --platform=$BUILDPLATFORM golang:1.27-alpine AS go-builder
ARG TARGETOS=linux
ARG TARGETARCH=amd64
ARG VERSION=dev
ARG COMMIT=none
ARG BUILD_DATE=unknown
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY cmd ./cmd
COPY internal ./internal
COPY --from=web-builder /src/internal/webui/dist ./internal/webui/dist
RUN CGO_ENABLED=0 GOOS=${TARGETOS} GOARCH=${TARGETARCH} \
    go build -trimpath \
    -ldflags "-s -w -X main.version=${VERSION} -X main.commit=${COMMIT} -X main.buildDate=${BUILD_DATE}" \
    -o /out/mt ./cmd/mt

FROM alpine:3.21 AS runtime
RUN apk add --no-cache ca-certificates tzdata \
    && addgroup -g 1000 -S mt \
    && adduser -u 1000 -S -G mt -h /app mt \
    && mkdir -p /app/data \
    && chown -R mt:mt /app

COPY --from=go-builder --chmod=755 /out/mt /usr/local/bin/mt

WORKDIR /app
USER mt

ENV TZ=Asia/Shanghai \
    MT_DB=/app/data/mt.db \
    MT_LISTEN=0.0.0.0:8080

EXPOSE 8080
VOLUME ["/app/data"]

HEALTHCHECK --interval=30s --timeout=5s --start-period=15s --retries=3 \
    CMD wget -qO- http://127.0.0.1:8080/healthz >/dev/null 2>&1 || exit 1

ENTRYPOINT ["/usr/local/bin/mt"]
CMD ["serve"]
