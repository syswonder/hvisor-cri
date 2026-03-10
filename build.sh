#!/usr/bin/env bash
set -euo pipefail

# 交叉编译目标（默认 arm64）
: "${GOOS:=linux}"
: "${GOARCH:=arm64}"

# 输出目录（默认拷贝到 ~/tftp）
: "${OUT_DIR:=$HOME/tftp}"

echo "[build] GOOS=${GOOS} GOARCH=${GOARCH}"
echo "[build] OUT_DIR=${OUT_DIR}"

# 编译 CRI
GOOS="${GOOS}" GOARCH="${GOARCH}" CGO_ENABLED=0 go build -o hvisor-cri main.go

# 编译 exec-helper（按次启动的 ExecSync helper）
GOOS="${GOOS}" GOARCH="${GOARCH}" CGO_ENABLED=0 go build -o hvisor-exec-helper exec-helper.go

mkdir -p "${OUT_DIR}"
rm -f "${OUT_DIR}/hvisor-cri" "${OUT_DIR}/hvisor-exec-helper"
cp -f hvisor-cri hvisor-exec-helper "${OUT_DIR}/"

echo "[build] done:"
ls -lh "${OUT_DIR}/hvisor-cri" "${OUT_DIR}/hvisor-exec-helper"