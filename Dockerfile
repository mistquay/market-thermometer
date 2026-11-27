# syntax=docker/dockerfile:1.7
# 标准三阶段多架构构建（零 QEMU，最终镜像基于 distroless/static:nonroot）：
#   Stage 1: 构建 React + Vite 前端产物 -> internal/webui/dist
#   Stage 2: 嵌入前端产物与时区库，纯 Go 静态交叉编译目标架构二进制并预备 rootfs
#   Stage 3: 最小化 distroless/static-debian12:nonroot（自带 CA 证书、非 root 65532、零 shell）

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
RUN mkdir -p /rootfs/app/data /rootfs/usr/local/bin \
    && CGO_ENABLED=0 GOOS=${TARGETOS} GOARCH=${TARGETARCH} \
       go build -trimpath \
       -ldflags "-s -w -X main.version=${VERSION} -X main.commit=${COMMIT} -X main.buildDate=${BUILD_DATE}" \
       -o /rootfs/usr/local/bin/mt ./cmd/mt \
    && chmod 755 /rootfs/usr/local/bin/mt \
    && chown -R 65532:65532 /rootfs/app

FROM gcr.io/distroless/static-debian12:nonroot AS runtime
COPY --from=go-builder /rootfs/ /

WORKDIR /app
USER nonroot:nonroot

ENV TZ=Asia/Shanghai \
    MT_DB=/app/data/mt.db \
    MT_LISTEN=0.0.0.0:8080

EXPOSE 8080
VOLUME ["/app/data"]

HEALTHCHECK --interval=30s --timeout=5s --start-period=15s --retries=3 \
    CMD ["/usr/local/bin/mt", "healthcheck"]

ENTRYPOINT ["/usr/local/bin/mt"]
CMD ["serve"]
