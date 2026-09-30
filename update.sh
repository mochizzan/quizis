#!/usr/bin/env bash
# update.sh — build ulang image aplikasi, dorong ke GHCR, lalu jalankan ulang compose.
#
# Urutan: compose down -> build (tag ghcr.io/mochizzan/quizis:latest) -> push -> compose up.
# Data MariaDB & upload tersimpan di bind mount ./data sehingga aman terhadap down/up.
#
# Pemakaian:
#   ./update.sh
#
# Autentikasi GHCR (push butuh token dengan scope write:packages):
#   GHCR_TOKEN=<token> ./update.sh            # login sesi lalu push
#   docker login ghcr.io -u <user> --password-stdin  # atau login manual sekali
set -euo pipefail

cd "$(dirname "$0")"

IMAGE="ghcr.io/mochizzan/quizis:latest"
GHCR_USER="${GHCR_USER:-mochizzan}"

echo "==> [1/4] docker compose down"
docker compose down

echo "==> [2/4] docker compose build ($IMAGE)"
docker compose build

echo "==> [3/4] push $IMAGE"
if [ -n "${GHCR_TOKEN:-}" ]; then
  echo "    login ghcr.io sebagai $GHCR_USER (GHCR_TOKEN terdeteksi)"
  printf '%s' "$GHCR_TOKEN" | docker login ghcr.io -u "$GHCR_USER" --password-stdin
else
  echo "    GHCR_TOKEN kosong — memakai sesi login docker yang sudah ada"
fi
docker push "$IMAGE"

echo "==> [4/4] docker compose up -d"
docker compose up -d

echo "==> Selesai. Aplikasi: http://localhost:8090"
docker compose ps
