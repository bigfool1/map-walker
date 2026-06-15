# ===== Build Stage =====
FROM golang:1.26-bookworm AS builder

ENV GOPROXY=https://goproxy.cn,direct \
    CGO_ENABLED=0

WORKDIR /app

# 先复制依赖文件，利用 Docker 层缓存
COPY go.mod go.sum ./
RUN go mod download

# 复制源码并编译
COPY . .
RUN go build -ldflags="-s -w" -o map-walker ./cmd/map-walker

# ===== Final Stage =====
FROM alpine:3.21

# ca-certificates 用于 HTTPS（地图 tile、CDN 等）
RUN apk add --no-cache ca-certificates tzdata

WORKDIR /app

# 从构建阶段复制二进制
COPY --from=builder /app/map-walker .

# 复制运行时静态资源
COPY web/ ./web/
COPY config/ ./config/

EXPOSE 8080

ENTRYPOINT ["./map-walker"]
CMD ["-host", "0.0.0.0", "-port", "8080"]
