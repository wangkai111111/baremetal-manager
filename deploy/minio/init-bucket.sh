#!/bin/sh
set -eu

mc alias set local "http://${MINIO_ENDPOINT}" "${MINIO_ROOT_USER}" "${MINIO_ROOT_PASSWORD}"
mc mb --ignore-existing "local/${MINIO_BUCKET}"
# 裸机镜像默认保持私有，由后端签发临时访问地址。
mc anonymous set none "local/${MINIO_BUCKET}"
echo "MinIO bucket is ready"
