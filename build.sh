#!/bin/bash
set -e

cd "$(dirname "$0")"

echo "=== 拉取最新代码 ==="
git pull

echo "=== 构建 Docker 镜像 ==="
docker compose build

echo "=== 构建完成 ==="
echo ""
echo "启动: docker compose up -d"
echo "首次启动前: cp .env.example .env && vim .env  # 修改密码"
echo "查看日志: docker compose logs -f"
