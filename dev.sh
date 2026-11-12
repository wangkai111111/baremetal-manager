#!/usr/bin/env bash
set -euo pipefail
ROOT="$(dirname "$(readlink -f "${BASH_SOURCE[0]}")")"
MODE="${1:-help}"

if [[ "$MODE" == "help" ]]; then
  printf '%s\n' '用法：bash dev.sh deps|migrate|backend|worker|scheduler|frontend' \
    'deps：仅启动 Docker 依赖，不停止现有服务、不删除数据卷' \
    'migrate：使用数据库 owner 执行迁移' \
    'backend / worker / scheduler：本地运行 Go 对应进程' \
    'frontend：运行 Vite 开发服务器（默认 5173）'
  exit 0
fi
case "$MODE" in deps|migrate|backend|worker|scheduler|frontend) ;; *) printf '%s\n' '未知启动模式，请运行 bash dev.sh help' >&2; exit 1 ;; esac
if [[ ! -f "$ROOT/.env" ]]; then
  printf '%s\n' '请先根据 .env.example 配置项目根目录 .env' >&2
  exit 1
fi
set -a
source "$ROOT/.env"
set +a

COMPOSE=(docker compose --project-directory "$ROOT" -f "$ROOT/docker-compose.yaml" -f "$ROOT/docker-compose.dev.yaml")
if [[ "$MODE" == "deps" ]]; then
  "${COMPOSE[@]}" up -d --wait postgres redis minio
  "${COMPOSE[@]}" run --rm minio-init
  exit 0
fi
if [[ "$MODE" == "frontend" ]]; then
  exec npm --prefix "$ROOT/frontend" run dev -- --port "${DEV_FRONTEND_PORT:-5173}" --strictPort
fi

# 仅修改连接地址，保留原连接串中的凭据与连接参数，不输出秘密。
DATABASE_URL="$(node -e '
const source = process.argv[1] === "migrate" ? process.env.DATABASE_MIGRATION_URL : process.env.DATABASE_URL;
if (!source) { console.error("缺少数据库连接配置"); process.exit(1); }
const u = new URL(source);
u.hostname = process.env.DEV_DATABASE_HOST || "127.0.0.1";
u.port = process.env.POSTGRES_PORT || "15432";
process.stdout.write(u.toString());
' "$MODE")"
export DATABASE_URL
export REDIS_URL="redis://127.0.0.1:${REDIS_PORT:-16379}/0"
export MINIO_ENDPOINT="127.0.0.1:${MINIO_PORT:-19000}"
export MINIO_ACCESS_KEY="${MINIO_ROOT_USER:-bmm-minio}"
export MINIO_SECRET_KEY="${MINIO_ROOT_PASSWORD:?请设置 MINIO_ROOT_PASSWORD}"
export MINIO_BUCKET="${MINIO_BUCKET:-baremetal-images}"
export SOL_ALLOWED_ORIGIN="${DEV_FRONTEND_ORIGIN:-http://localhost:${DEV_FRONTEND_PORT:-5173}}"
export SEED_DATA=false
if [[ "$MODE" == "backend" ]]; then MODE=api; fi
exec go -C "$ROOT/backend" run ./cmd/server "$MODE"
