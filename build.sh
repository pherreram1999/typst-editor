#!/usr/bin/env bash
# Compila el frontend y el servidor y deja el resultado en la raíz del repositorio:
#   ./typst-editor   binario del servidor (API + frontend)
#   ./public/        frontend compilado que sirve el binario
#
# Uso: ./build.sh [--skip-install]
#   --skip-install   no ejecuta npm ci (usa el node_modules existente)
# Variables opcionales: GOOS, GOARCH (compilación cruzada), CGO_ENABLED (por defecto 0).
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
BIN="$ROOT/typst-editor"
PUBLIC="$ROOT/public"

skip_install=0
for arg in "$@"; do
  case "$arg" in
    --skip-install) skip_install=1 ;;
    -h|--help) sed -n '2,8p' "$0" | sed 's/^# \{0,1\}//'; exit 0 ;;
    *) echo "opción desconocida: $arg" >&2; exit 2 ;;
  esac
done

for cmd in go npm; do
  command -v "$cmd" >/dev/null || { echo "error: no se encuentra '$cmd' en el PATH" >&2; exit 1; }
done

echo "==> Frontend"
cd "$ROOT/frontend"
if [[ $skip_install -eq 0 ]]; then
  npm ci --no-audit --no-fund
fi
npm run build
rm -rf "$PUBLIC"
cp -r "$ROOT/frontend/dist" "$PUBLIC"

echo "==> Servidor"
cd "$ROOT/backend"
version="$(git -C "$ROOT" describe --tags --always --dirty 2>/dev/null || echo dev)"
CGO_ENABLED="${CGO_ENABLED:-0}" go build -trimpath -ldflags "-s -w" -o "$BIN" .

echo
echo "Listo ($version):"
echo "  $BIN"
echo "  $PUBLIC/"
echo
echo "Para ejecutarlo, desde la raíz (lee .env del directorio actual):"
echo "  cp backend/.env.example .env   # y edítalo, o copia tu backend/.env"
echo "  ./typst-editor"
