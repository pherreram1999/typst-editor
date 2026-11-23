#!/usr/bin/env bash
# Compila la imagen de Docker y la sube a GitHub Container Registry con dos
# etiquetas: la versión indicada y «latest».
#   ghcr.io/runtime-team/typst-extend:<versión>
#   ghcr.io/runtime-team/typst-extend:latest
#
# Uso: ./push-image.sh <versión> [--no-push]
#   <versión>   p. ej. 1.4.0 o v1.4.0 (se usa tal cual como etiqueta)
#   --no-push   solo compila y etiqueta, sin subir
# Variables opcionales: IMAGE (por defecto ghcr.io/runtime-team/typst-extend),
#   MS_FONTS (1 por defecto; 0 para no incluir las fuentes de Microsoft).
# Requiere haber iniciado sesión: docker login ghcr.io
#
# Las fuentes de Microsoft no se pueden redistribuir: con MS_FONTS=1 el
# paquete de ghcr.io debe ser privado.
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
IMAGE="${IMAGE:-ghcr.io/runtime-team/typst-extend}"
MS_FONTS="${MS_FONTS:-1}"

version=""
push=1
for arg in "$@"; do
  case "$arg" in
    --no-push) push=0 ;;
    -h|--help) sed -n '2,15p' "$0" | sed 's/^# \{0,1\}//'; exit 0 ;;
    -*) echo "opción desconocida: $arg" >&2; exit 2 ;;
    *)
      [[ -z "$version" ]] || { echo "error: solo se admite una versión" >&2; exit 2; }
      version="$arg" ;;
  esac
done

[[ -n "$version" ]] || { echo "uso: $0 <versión> [--no-push]" >&2; exit 2; }
# Formato de etiqueta de Docker: letras, dígitos, «_», «.» y «-», máx. 128.
if [[ ! "$version" =~ ^[A-Za-z0-9_][A-Za-z0-9_.-]{0,127}$ ]]; then
  echo "error: «$version» no es una etiqueta de Docker válida" >&2
  exit 2
fi
if [[ "$version" == latest ]]; then
  echo "error: indica una versión concreta; «latest» se añade siempre" >&2
  exit 2
fi

command -v docker >/dev/null || { echo "error: no se encuentra 'docker' en el PATH" >&2; exit 1; }

cd "$ROOT"
commit="$(git rev-parse --short HEAD 2>/dev/null || echo desconocido)"
if [[ -n "$(git status --porcelain 2>/dev/null)" ]]; then
  echo "aviso: hay cambios sin commit; la imagen incluye el árbol de trabajo actual" >&2
  commit="$commit-modificado"
fi

echo "==> Compilando $IMAGE:$version (commit $commit, MS_FONTS=$MS_FONTS)"
docker build \
  --pull \
  --build-arg MS_FONTS="$MS_FONTS" \
  --label org.opencontainers.image.title=typst-extend \
  --label org.opencontainers.image.version="$version" \
  --label org.opencontainers.image.revision="$commit" \
  --label org.opencontainers.image.created="$(date -u +%Y-%m-%dT%H:%M:%SZ)" \
  -t "$IMAGE:$version" \
  -t "$IMAGE:latest" \
  .

if [[ $push -eq 0 ]]; then
  echo "==> Listo (sin subir): $IMAGE:$version y $IMAGE:latest"
  exit 0
fi

echo "==> Subiendo $IMAGE:$version"
docker push "$IMAGE:$version"
echo "==> Subiendo $IMAGE:latest"
docker push "$IMAGE:latest"
echo "==> Publicadas $IMAGE:$version y $IMAGE:latest"
