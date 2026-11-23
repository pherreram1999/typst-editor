#!/bin/sh
# Arranque de la imagen. Si el contenedor arranca como root (lo normal), deja
# DATA_DIR en manos del usuario «app» y ejecuta el servidor como ese usuario,
# sin privilegios. Hace falta porque algunas plataformas montan el volumen
# como root (p. ej. Bunny Magic Containers) y el servidor no podría escribir.
# Si ya arranca sin root (docker run --user …), se ejecuta tal cual.
set -e
DATA_DIR="${DATA_DIR:-/data}"
if [ "$(id -u)" = 0 ]; then
  mkdir -p "$DATA_DIR"
  # Solo lo que no sea ya de app: rápido aunque haya muchos proyectos.
  find "$DATA_DIR" -xdev ! -user app -exec chown app:app {} + 2>/dev/null || true
  exec su-exec app /app/typst-editor "$@"
fi
exec /app/typst-editor "$@"
