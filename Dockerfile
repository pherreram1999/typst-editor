# syntax=docker/dockerfile:1
#
# Imagen de nullpointer editor: servidor Go (API + frontend compilado) + typst + git.
#
#   docker build -t typst-editor .
#   docker build --build-arg MS_FONTS=0 -t typst-editor .   # sin fuentes de Microsoft
#
# Volumen: /data (proyectos, compilaciones, fuentes locales y caché de paquetes).
# Puerto:  8080.
# Base de datos: DB_DRIVER=mysql (MariaDB; crea DB_NAME si no existe) o libsql
# (Bunny Database: DB_URL + DB_AUTH_TOKEN). El esquema se migra al arrancar
# (idempotente: añade columnas y tablas nuevas a una base existente, p. ej.
# users.email, account_tokens y project_email_invites, sin tocar los datos);
# «docker run … typst-editor migrate» solo migra y termina.
# Correo (Resend): RESEND (clave), MAIL_FROM (remitente de un dominio
# verificado) y PUBLIC_URL (URL pública: la usan los enlaces de los correos).
# MAIL_ENABLED=false lo desactiva: las cuentas nuevas reciben una contraseña
# temporal que deben cambiar al entrar.
# ADMIN_EMAIL asigna el correo del administrador inicial.

# La versión menor de typst debe coincidir con el compilador WASM del frontend
# (@myriaddreamin/typst-ts-* 0.8.0-rc4 = typst 0.15) para que la vista previa
# en el navegador pagine igual que el PDF.
ARG TYPST_VERSION=0.15.1
ARG ALPINE_VERSION=3.23

# --- Frontend ---------------------------------------------------------------
FROM --platform=$BUILDPLATFORM node:26-alpine AS frontend
WORKDIR /src/frontend
COPY frontend/package.json frontend/package-lock.json ./
RUN npm ci --no-audit --no-fund
COPY frontend/ ./
RUN npm run build

# --- Servidor (binario estático) --------------------------------------------
FROM --platform=$BUILDPLATFORM golang:1.27-alpine AS backend
ARG TARGETOS TARGETARCH
WORKDIR /src/backend
COPY backend/go.mod backend/go.sum ./
RUN go mod download
COPY backend/ ./
RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH \
    go build -trimpath -ldflags "-s -w" -o /out/typst-editor .

# --- typst (binario oficial, musl estático) ---------------------------------
FROM alpine:${ALPINE_VERSION} AS typst
ARG TYPST_VERSION TARGETARCH
RUN set -eu; \
    case "$TARGETARCH" in \
      amd64) arch=x86_64 ;; \
      arm64) arch=aarch64 ;; \
      *) echo "arquitectura no soportada: $TARGETARCH" >&2; exit 1 ;; \
    esac; \
    name="typst-$arch-unknown-linux-musl"; \
    wget -qO- "https://github.com/typst/typst/releases/download/v${TYPST_VERSION}/$name.tar.xz" \
      | tar -xJ -C /tmp; \
    install -m 0755 "/tmp/$name/typst" /usr/local/bin/typst; \
    /usr/local/bin/typst --version

# --- Fuentes ----------------------------------------------------------------
# MS_FONTS=1: «Core fonts for the Web» de Microsoft (Arial, Times New Roman,
# Courier New, Georgia, Verdana, Trebuchet MS, Comic Sans MS, Impact, Andale
# Mono, Webdings), descargadas de SourceForge al construir. Su licencia permite
# usarlas pero no redistribuirlas sueltas: no publiques la imagen resultante.
# Calibri no forma parte del paquete; se añade Carlito, que tiene sus mismas
# métricas (el texto ocupa lo mismo; también se usa con MS_FONTS=0).
FROM alpine:${ALPINE_VERSION} AS fonts
ARG MS_FONTS=1
RUN set -eu; mkdir -p /fonts; \
    apk add --no-cache font-carlito; \
    cp /usr/share/fonts/carlito/*.ttf /fonts/; \
    if [ "$MS_FONTS" = 1 ]; then \
      apk add --no-cache msttcorefonts-installer; \
      update-ms-fonts; \
      mkdir -p /fonts/ms; \
      find /usr/share/fonts/truetype/msttcorefonts -type f -name "*.ttf" -exec cp {} /fonts/ms/ +; \
      test "$(ls /fonts/ms | wc -l)" -ge 20; \
    fi

# --- Imagen final -----------------------------------------------------------
FROM alpine:${ALPINE_VERSION}
RUN apk add --no-cache git ca-certificates su-exec \
 && adduser -D -H -u 10001 -h /data app \
 && mkdir -p /data && chown app:app /data
COPY --from=typst /usr/local/bin/typst /usr/local/bin/typst
COPY --from=fonts /fonts /opt/fonts
WORKDIR /app
COPY fonts/ ./fonts/
COPY --from=frontend /src/frontend/dist ./public/
COPY --from=backend /out/typst-editor ./typst-editor
COPY --chmod=0755 docker-entrypoint.sh /usr/local/bin/docker-entrypoint.sh

ENV ADDR=:8080 \
    DATA_DIR=/data \
    HOME=/data \
    XDG_CACHE_HOME=/data/cache \
    XDG_DATA_HOME=/data/share \
    FONT_PATHS=/opt/fonts

# Arranca como root solo para dar /data al usuario «app» (algunas plataformas,
# como Bunny Magic Containers, montan el volumen como root) y enseguida
# ejecuta el servidor como «app» (uid 10001) con su-exec: ver docker-entrypoint.sh.
VOLUME /data
EXPOSE 8080
HEALTHCHECK --interval=30s --timeout=3s --start-period=10s \
  CMD wget -qO- "http://127.0.0.1:${ADDR##*:}/api/health" >/dev/null || exit 1
ENTRYPOINT ["/usr/local/bin/docker-entrypoint.sh"]
