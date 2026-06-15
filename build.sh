#!/bin/bash
set -e

cd "$(dirname "$0")"

echo "=== 构建 map-walker Docker 镜像 ==="
docker compose build

echo ""
echo "=== 构建完成 ==="
echo "启动: docker compose up -d"
echo "查看日志: docker compose logs -f"
