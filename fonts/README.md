# Fuentes incluidas

El servidor añade esta carpeta (junto a `DATA_DIR/fonts` y las de `FONT_PATHS`)
a las fuentes que ve typst, en la compilación del servidor y en la vista previa
del navegador, para todos los proyectos y usuarios.

- `roboto/` — Roboto 2.138 (Apache 2.0, ver `roboto/LICENSE`).
- `roboto-mono/` — Roboto Mono 3.001 (SIL OFL 1.1, ver `roboto-mono/OFL.txt`).

Aquí solo van fuentes con licencia libre, porque se versionan. Las fuentes con
licencia propietaria (Calibri, Segoe UI…) se copian en `DATA_DIR/fonts` del
servidor, que no se sube a git; se detectan sin reiniciar.
