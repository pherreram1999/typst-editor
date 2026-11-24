# Typst Extend

Editor web colaborativo de documentos [Typst](https://typst.app): varias personas editan el mismo documento a la vez, ven la vista previa y el PDF al instante y sincronizan el proyecto con GitHub.

## Funcionalidades

- **Edición simultánea sin conflictos.** CodeMirror 6 + `@codemirror/collab` (Operational Transformation con autoridad central) sobre WebSocket, con cursores y presencia de los demás usuarios.
- **Vista previa en vivo y visor PDF**, generados con el binario `typst` del sistema. Los errores de compilación enlazan a la línea del código.
- **Subida de archivos con Uppy** (arrastrar y soltar, carpeta de destino), **reemplazo** de imágenes y **optimización sin pérdida** de PNG/JPEG: mismos píxeles y mismo tamaño en el documento, pero mucho menos peso (los diagramas suelen bajar a la cuarta parte).
- **Sincronización vista previa ↔ código:** un clic en la vista previa lleva al párrafo correspondiente del código y un clic en el código desplaza la vista previa hasta ese bloque y lo resalta.
- **Documentos grandes** (tesis de cientos de páginas con muchas capturas): la vista previa se compila con copias reducidas de las imágenes que conservan su tamaño en la página, así que la paginación es la misma que la del PDF (que usa los originales). El navegador solo descarga y muestra las páginas cercanas a la vista y, al recompilar, solo las que cambiaron. `typst` corre con prioridad baja y con un límite de procesos simultáneos.
- **Revisión ortográfica en español de México.** Subraya en el editor las palabras que no están en el diccionario, solo en la prosa (no en código, matemáticas, cadenas, raw, etiquetas ni comentarios). Clic derecho (o un toque en el celular) muestra sugerencias, «Agregar al diccionario» (diccionario personal del navegador) e «Ignorar». Se activa o desactiva en «Más» de la barra del editor. Todo ocurre en el navegador, en un *Web Worker*: el texto no sale del equipo.
- **Proyectos multiarchivo** (`.typ`, `.bib`, imágenes…). Cada proyecto es un repositorio git en `DATA_DIR/projects/<id>`.
- **Plantilla por defecto** en tamaño carta (`us-letter`) y español de México (`lang: "es", region: "mx"`).
- **Plantillas y copias.** Cualquier proyecto se puede duplicar. El propietario puede publicarlo como plantilla con categoría y descripción (p. ej. distintas carátulas). Las plantillas aparecen para todos en una galería con miniatura y se usan creando una copia.
- **Proyectos compartidos.** El propietario invita a usuarios registrados como colaboradores con permiso de edición.
- **Organización personal.** Cada usuario destaca proyectos (siempre aparecen primero) y los agrupa en carpetas con color. Nadie más ve esa organización.
- **Sincronización con GitHub.** Cada usuario registra sus cuentas (usuario + token). Al crear un proyecto puede buscar entre sus repositorios y cargar uno. *Pull* y *push* son manuales y usan el binario `git` del sistema.
- **Usuarios y roles** (`admin` / `user`) con autenticación JWT.
- **Agentes (MCP).** Un servidor [Model Context Protocol](https://modelcontextprotocol.io) en `/mcp` (SDK oficial de Go) permite que Claude, ChatGPT, Cursor o Claude Code listen y creen proyectos, lean y editen archivos (en vivo, junto a los demás editores), suban y descarguen archivos, compilen, vean páginas renderizadas y obtengan el PDF. Para conectores remotos usa OAuth 2.1; en local, tokens personales. Ver [Agentes (MCP)](#agentes-mcp).

## Tecnologías

| Capa | Tecnología |
|---|---|
| Frontend | Svelte 5, Vite 8, Tailwind CSS 4, CodeMirror 6 |
| Backend | Go (`net/http`), WebSocket (`coder/websocket`), JWT HS256, bcrypt |
| Base de datos | MariaDB (probado con 11.8; las migraciones usan `ALTER TABLE … IF NOT EXISTS`) o libSQL remoto (Bunny Database, Turso, sqld), según `DB_DRIVER` |
| Herramientas del sistema | `typst` (≥ 0.12), `git` |

Identidad visual: color principal blue-800 (`#1E40AF`), neutros slate y tipografía Inter. Los tokens están en `frontend/src/app.css`.

## Requisitos

- Go 1.27 o superior (ver `backend/go.mod`).
- Node.js en versión LTS reciente (probado con Node 26).
- MariaDB, o una base libSQL remota (Bunny Database, Turso, sqld).
- `typst` y `git` en el `PATH`, o sus rutas en `TYPST_BIN` / `GIT_BIN`.

## Puesta en marcha

### 1. Base de datos

```bash
# Cambia antes la contraseña del usuario 'typsteditor' en el script
sudo mariadb < database/01_setup.sql
```

**Con libSQL (Bunny Database)** no hace falta el script: crea la base en el panel de Bunny (*Database → Access*), copia la URL y genera un token. En `.env`:

```bash
DB_DRIVER=libsql
DB_URL=libsql://<id-de-la-base>.lite.bunnydb.net
DB_AUTH_TOKEN=<token>
```

El servidor crea las tablas al arrancar. En libSQL, la búsqueda de usuarios solo ignora mayúsculas en letras sin acento y los nombres se ordenan por bytes. En MariaDB, la intercalación `utf8mb4_unicode_ci` ignora mayúsculas y acentos.

### 2. Backend

```bash
cd backend
cp .env.example .env
go run . key:generate      # escribe un JWT_SECRET aleatorio en .env
# edita .env: DB_PASSWORD y ADMIN_PASSWORD (mín. 8 caracteres)
go run .                   # API en http://localhost:8080
```

Al arrancar:
- crea o migra las tablas;
- crea el primer administrador con `ADMIN_USER` / `ADMIN_PASSWORD` si no existe ninguno;
- avisa si no encuentra `typst` o `git`.

### 3. Frontend

```bash
cd frontend
npm install
npm run dev                # http://localhost:5173 (proxy de /api y del WebSocket a :8080)
```

Para apuntar el proxy a otro backend: `API_TARGET=http://127.0.0.1:18080 npm run dev`.

### Producción

El servidor de Go sirve también el frontend compilado, así que basta un solo proceso. `build.sh` compila todo y deja el resultado en la raíz del repositorio (ignorado por git):

```bash
./build.sh                    # npm ci + vite build + go build  (--skip-install para no reinstalar node_modules)
cp backend/.env.example .env  # o copia tu backend/.env; se lee del directorio actual
./typst-editor                # app + API en http://localhost:8080
```

Genera `./typst-editor` (binario estático, `CGO_ENABLED=0`; admite `GOOS`/`GOARCH`) y `./public/` (frontend). Con las rutas por defecto, los proyectos se guardan en `./data/`.

Si `STATIC_DIR` está vacío, el servidor usa `public/` junto al ejecutable y, si no existe, `../frontend/dist` (el caso de `go run .` desde `backend/`). Los archivos de `assets/` se envían con caché permanente (llevan hash en el nombre) e `index.html` con `no-cache`. Cualquier ruta que no sea un archivo devuelve `index.html`; las rutas `/api/` desconocidas devuelven 404 en JSON. Si no encuentra `index.html`, avisa al arrancar y solo sirve la API.

### Docker

El `Dockerfile` de la raíz compila frontend y servidor en etapas separadas. La imagen final se basa en Alpine e incluye el binario, `public/`, `fonts/`, el `typst` oficial (musl, versión fijada en `TYPST_VERSION`) y `git`. Pesa unos 60 MB comprimida y 196 MB en disco.

```bash
docker build -t typst-editor .                          # con las fuentes de Microsoft
docker build --build-arg MS_FONTS=0 -t typst-editor .   # sin ellas
```

**Fuentes incluidas:**
- **Microsoft** (`MS_FONTS=1`, por defecto): las *Core fonts for the Web* (Arial, Arial Black, Times New Roman, Courier New, Georgia, Verdana, Trebuchet MS, Comic Sans MS, Impact, Andale Mono, Webdings). Se descargan de SourceForge al construir la imagen.
- **Carlito**: misma métrica que Calibri, así que el texto ocupa lo mismo.
- Las del repositorio (Roboto, Roboto Mono) y las que trae `typst`.

Calibri, Cambria y el resto de fuentes de Office no se pueden descargar libremente. Si tienes licencia, copia los `.ttf`/`.otf` a `fonts/` dentro del volumen (`/data/fonts`); el servidor las detecta sin reiniciar.

> La licencia de las fuentes de Microsoft permite usarlas, pero no redistribuirlas fuera de sus instaladores originales. Usa la imagen en tu servidor y no la publiques en un registro público; si necesitas publicarla, constrúyela con `MS_FONTS=0`.

**Volumen:** monta **`/data`**. Ahí se guardan:
- `projects/<id>`: los proyectos, cada uno es un repositorio git;
- `build/`: compilaciones y vista previa (se regeneran);
- `review/`: enlaces de revisión;
- `fonts/`: fuentes locales;
- `packages/` y `cache/`: paquetes de Typst descargados.

Sin este volumen, los proyectos se pierden al recrear el contenedor. El proceso corre como el usuario `app` (UID/GID `10001`). Un volumen con nombre ya tiene los permisos correctos. Si usas una carpeta del host, dásela antes a ese usuario: `sudo chown -R 10001:10001 /srv/typst-editor`.

**Puerto:** **`8080`** (HTTP; sirve la app, la API y el WebSocket). Se cambia con `ADDR` y el *healthcheck* (`/api/health`) lo sigue.

**Base de datos:** la imagen no incluye ninguna base. Al arrancar, el contenedor migra el esquema del motor que indique `DB_DRIVER`:
- **MariaDB** (`DB_DRIVER=mysql`, por defecto): si la base `DB_NAME` no existe, la crea (utf8mb4) y después crea las tablas. Para eso el usuario de `DB_USER` necesita el privilegio `CREATE`. Si no lo tiene, crea antes la base y el usuario con `database/01_setup.sql`.
- **libSQL / Bunny Database** (`DB_DRIVER=libsql`): la base se crea en el panel de Bunny, porque el protocolo no permite crearla. Las tablas las crea el contenedor.

Para migrar sin arrancar el servidor (por ejemplo, en un paso previo al despliegue): `docker run --rm --env-file typst.env typst-editor migrate`.

La configuración se pasa con variables de entorno o con `--env-file`; dentro del contenedor no hace falta un `.env`. `DATA_DIR`, `HOME` y las cachés ya apuntan a `/data`.

```bash
docker volume create typst-editor-data
docker run -d --name typst-editor --restart unless-stopped \
  -p 8080:8080 \
  -v typst-editor-data:/data \
  -e DB_HOST=db.ejemplo.local -e DB_NAME=typsteditor -e DB_USER=typsteditor -e DB_PASSWORD='…' \
  -e JWT_SECRET="$(openssl rand -base64 48)" \
  -e ADMIN_PASSWORD='…' \
  typst-editor
```

Con Bunny Database, cambia las variables de MariaDB por:

```bash
  -e DB_DRIVER=libsql -e DB_URL=libsql://<id-de-la-base>.lite.bunnydb.net -e DB_AUTH_TOKEN='…' \
```

Guarda el `JWT_SECRET` (por ejemplo en un `--env-file`): si cambia, se cierran todas las sesiones.

Si MariaDB corre en el mismo host, `127.0.0.1` dentro del contenedor no es el host. Tienes dos opciones:
- `--network host`, y entonces no hace falta `-p`;
- `--add-host=host.docker.internal:host-gateway -e DB_HOST=host.docker.internal`, con un usuario de MariaDB que acepte conexiones desde la red de Docker.

Detrás de un proxy inverso, reenvía también el WebSocket (`Upgrade` / `Connection`) y usa HTTPS.

## Configuración (`backend/.env`)

| Variable | Por defecto | Descripción |
|---|---|---|
| `ADDR` | `:8080` | Dirección de escucha (API y frontend) |
| `DB_DRIVER` | `mysql` | Motor de base de datos: `mysql` (MariaDB) o `libsql` |
| `DB_HOST`, `DB_PORT`, `DB_NAME`, `DB_USER`, `DB_PASSWORD` | `127.0.0.1`, `3306`, `typsteditor`, `typsteditor`, — | Conexión a MariaDB (`DB_DRIVER=mysql`) |
| `DB_URL`, `DB_AUTH_TOKEN` | `BUNNY_DATABASE_URL`, `BUNNY_DATABASE_AUTH_TOKEN` | Conexión a libSQL (`DB_DRIVER=libsql`): `libsql://…`, `https://…` o `http://host:puerto`; un `?authToken=` en la URL también vale |
| `JWT_SECRET` | — | Obligatorio, mínimo 32 caracteres (`go run . key:generate`) |
| `TOKEN_TTL` | `8h` | Duración de la sesión |
| `ADMIN_USER`, `ADMIN_NAME`, `ADMIN_PASSWORD` | `admin`, `Administrador`, — | Primer administrador (solo si no existe ninguno) |
| `DATA_DIR` | `data` | Proyectos (`projects/<id>`, repos git) y compilaciones (`build/<id>`) |
| `TYPST_BIN`, `GIT_BIN` | `typst`, `git` | Binarios del sistema |
| `GITHUB_API` | `https://api.github.com` | Cámbiala para GitHub Enterprise (`https://<host>/api/v3`) |
| `STATIC_DIR` | `public/` junto al ejecutable, o `../frontend/dist` | Frontend compilado que sirve el propio servidor |
| `PUBLIC_URL` | `http://localhost:<puerto de ADDR>` | URL pública, sin barra final: emisor OAuth y recurso MCP (`PUBLIC_URL/mcp`). Para conectores remotos debe ser https y accesible desde internet |
| `MCP_ENABLED` | `true` | Servidor MCP (`/mcp`) y servidor OAuth (`/oauth/*`) |

Las variables de entorno reales tienen prioridad sobre `.env`.

## Comandos de desarrollo

```bash
# backend/
go run .                         # servidor (equivale a `go run . serve`)
go run . key:generate [--show]   # generar JWT_SECRET (--show solo lo imprime)
go build -o bin/server .         # binario
go vet ./...
go test ./...                    # unitarios + integración de git (repos bare locales)
go test ./internal/collab -run TestApplyChangeSet

# frontend/
npm run build                    # producción en dist/
npm run preview                  # sirve dist/ con el mismo proxy
```

## Estructura

```
backend/
  main.go                 subcomandos (serve, key:generate)
  internal/
    config/               .env y DSN
    database/             conexión y migraciones idempotentes
    auth/                 emisión y validación de JWT
    httpapi/              rutas, middleware y handlers (proyectos, miembros, carpetas, plantillas, GitHub, WebSocket)
    collab/               autoridad OT: documentos abiertos, versiones, presencia
    typst/                invocación de `typst compile` (SVG, PDF, miniatura) y copias reducidas de imágenes para la vista previa
    gitsync/              pull/push con `git` (token como cabecera HTTP)
    github/               cliente mínimo de la API REST de GitHub
frontend/src/
  App.svelte              enrutado por hash (#/, #/p/<id>, #/github, #/usuarios)
  routes/                 páginas (Proyectos, Workspace del editor, cuentas GitHub, usuarios)
  lib/                    cliente API, sesión, componentes y cliente colaborativo de CodeMirror
database/01_setup.sql     creación de BD, usuario y tablas
```

## Cómo funciona

### Edición colaborativa

El servidor (`backend/internal/collab`) es la autoridad. Guarda el texto en unidades UTF-16, igual que CodeMirror, y lleva una versión lineal del documento. Solo acepta cambios basados en la versión actual. Si un cliente se queda atrás, primero recibe los cambios que le faltan, reajusta (*rebase*) los suyos con `receiveUpdates` y vuelve a enviarlos. Así todos convergen al mismo texto.

Mensajes del protocolo WebSocket:
- **Cliente → servidor:** `push` (`{version, updates:[{clientID, changes}]}`) y `cursor`.
- **Servidor → cliente:**
  - `init` (documento completo + versión);
  - `resume` (reconexión que conserva lo escrito sin conexión);
  - `updates` (cambios nuevos);
  - `pushed` (`ok:false` si la versión estaba desfasada);
  - `cursor` y `cursor-leave` (cursores de los demás);
  - `presence` (quién está conectado);
  - `rev` (el proyecto cambió: recompilar);
  - `members` (cambió la lista de miembros);
  - `closed` (`revoked: true` si se retiró el acceso).

### Revisión ortográfica

El diccionario es el de [RLA-ES](https://github.com/sbosio/rla-es) para español de México (el de LibreOffice), del paquete `dictionary-es-mx` (licencia GPL-3.0 / LGPL-3.0 / MPL-1.1). Al compilar el frontend, `frontend/scripts/spell-dict.mjs` (plugin de Vite) lo expande con `nspell` a sus ~550 000 formas (conjugaciones, plurales, pronombres enclíticos como «dígaselo», nombres propios) y las guarda en `frontend/src/lib/spell/es-MX.words.txt` con codificación frontal: 2,2 MB, ~300 KB comprimido, que el navegador descarga una vez por versión. El archivo generado no se versiona.

Un *Web Worker* (`spell.worker.js`) busca cada palabra con búsqueda binaria y propone correcciones por distancia de edición, dando preferencia a los errores típicos del español (acentos, b/v, c/s/z, g/j, h muda, «que/gue»). Las palabras con mayúscula inicial se aceptan si existen en minúscula, y las siglas (hasta 5 mayúsculas), las palabras junto a cifras, las extensiones y los dominios no se revisan. El diccionario personal se guarda en el navegador (`localStorage`).

Los cambios externos (un *pull*, o subir un archivo que está abierto) se emiten como una actualización más del servidor. Los editores abiertos los reciben sin perder sus cambios pendientes. Los documentos se guardan en disco con un breve retardo. Compilar, duplicar o hacer operaciones git primero vuelca a disco lo que haya en memoria.

### GitHub

1. En **GitHub** (barra superior) cada usuario añade sus cuentas: usuario + token de acceso personal.
   - Sirve un *fine-grained* con «Contents: Read and write» o un *classic* con `repo`.
   - El token se valida contra `GET /user` y debe pertenecer a ese usuario.
   - Se guarda en la tabla `github_accounts`; nunca se devuelve por la API ni se escribe en `.git/config`.
2. **Nuevo proyecto → Desde GitHub:** se elige la cuenta, se busca el repositorio y se indica la rama. Si el repositorio está vacío, el proyecto empieza con la plantilla y el primer *push* crea la rama.
3. **Operaciones:**
   - **Pull:** hace commit de los cambios locales y luego `fetch` + `merge`. Si hay conflicto, aborta la fusión e indica los archivos afectados.
   - **Push:** hace commit con el mensaje indicado (autor = usuario actual) y `push`. Si el remoto tiene commits nuevos, pide hacer *pull* antes.

Solo se aceptan remotos `https://`.

### Plantillas

Un proyecto marcado como plantilla aparece en la galería **Plantillas** para todos los usuarios, con miniatura de su primera página. «Usar plantilla» y «Duplicar» crean un proyecto nuevo del usuario con una copia de los archivos de trabajo. La copia tiene su propio historial git y no hereda remoto, cuenta de GitHub ni colaboradores. Quien no tiene acceso a la plantilla puede copiarla, pero no abrirla ni editarla.

## Permisos

| Acción | Propietario | Miembro invitado | Admin | Cualquier usuario |
|---|---|---|---|---|
| Ver, editar, compilar, subir o borrar archivos, pull/push | ✓ | ✓ | ✓ | — |
| Ajustes (nombre, archivo principal, GitHub, plantilla), eliminar, invitar/quitar | ✓ | — | ✓ | — |
| Duplicar el proyecto | ✓ | ✓ | ✓ | — |
| Ver la miniatura y crear una copia, si es plantilla | ✓ | ✓ | ✓ | ✓ |
| Abandonar el proyecto | — | ✓ | — | — |

- Sin acceso, la API responde 404 para no revelar qué proyectos existen.
- Al quitar a un miembro se cierran al instante sus sesiones de edición.
- Los miembros hacen pull/push con la cuenta de GitHub asignada al proyecto, sin ver nunca el token.

## Agentes (MCP)

El servidor expone sus funciones a agentes de IA por [MCP](https://modelcontextprotocol.io) (transporte *Streamable HTTP*, sin sesión) en `PUBLIC_URL/mcp`. Corre dentro del mismo proceso que la API: las ediciones del agente pasan por el hub colaborativo, así que quien tenga el archivo abierto las ve al instante y la vista previa se recompila.

### Conectar un agente

La URL del servidor MCP es siempre `PUBLIC_URL/mcp`, y la página **Agentes** de la web la muestra con un botón para copiarla. Hay dos formas de autenticarse:

| | Token personal | OAuth 2.1 |
|---|---|---|
| Para | clientes en tu máquina: Claude Code, Cursor, VS Code, scripts | conectores remotos: claude.ai, Claude Desktop, ChatGPT (también Claude Code) |
| Cómo | lo creas en **Agentes** y lo pegas en el cliente | el cliente se registra solo y te pide autorizar en el navegador |
| `PUBLIC_URL` | cualquiera (vale `http://localhost:8080`) | https y accesible desde internet (los clientes locales con OAuth aceptan `localhost`) |

#### 1. Preparar `PUBLIC_URL`

`PUBLIC_URL` es la dirección con la que los clientes llegan al servidor, sin barra final. Es el emisor de OAuth y el recurso al que se ligan los tokens, así que **debe coincidir exactamente** con la URL que pongas en el cliente: `http://localhost:8080` y `http://127.0.0.1:8080` no son la misma. Reinicia el servidor tras cambiarla; al arrancar, el log muestra `MCP: <PUBLIC_URL>/mcp`.

- **Solo en local:** déjala vacía y se usa `http://localhost:<puerto de ADDR>`.
- **Conector remoto en desarrollo**, con un túnel:
  ```bash
  cloudflared tunnel --url http://localhost:8080
  # → https://algo-aleatorio.trycloudflare.com
  ```
  En `backend/.env` pon `PUBLIC_URL=https://algo-aleatorio.trycloudflare.com` y reinicia. Los túneles rápidos cambian de URL en cada ejecución, así que entonces hay que actualizar `PUBLIC_URL` y volver a añadir el conector. `ngrok http 8080` funciona igual.
- **Producción:** usa el dominio https del proxy inverso, p. ej. `PUBLIC_URL=https://typst.midominio.com`. El proxy debe reenviar `/mcp`, `/oauth/` y `/.well-known/` al backend, sin buffering en `/mcp` y con un tiempo de espera largo (compilar puede tardar minutos). Ver [Detrás de un proxy inverso](#detrás-de-un-proxy-inverso).

Comprobación rápida: `curl -i PUBLIC_URL/mcp` debe responder `401` con una cabecera `WWW-Authenticate` que apunta a `PUBLIC_URL/.well-known/oauth-protected-resource/mcp`.

#### 2a. Con token personal

1. En la web abre **Agentes** › «Crear token personal».
2. Elige el nombre, el permiso (lectura o lectura y escritura), los proyectos y la caducidad.
3. Copia el token (`tse_…`). Se muestra una sola vez; si lo pierdes, revócalo y crea otro.

**Claude Code** (el comando aparece ya relleno al crear el token):
```bash
claude mcp add --transport http typst-extend http://localhost:8080/mcp \
  --header "Authorization: Bearer tse_…"
# --scope user para todos tus proyectos, --scope project para compartirlo en .mcp.json
claude mcp list        # debe mostrar typst-extend como conectado
```

**Cursor** (`~/.cursor/mcp.json` o `.cursor/mcp.json`), **Claude Code** (`.mcp.json`) y otros clientes con el mismo formato:
```json
{
  "mcpServers": {
    "typst-extend": {
      "type": "http",
      "url": "http://localhost:8080/mcp",
      "headers": { "Authorization": "Bearer tse_…" }
    }
  }
}
```

**VS Code** (`.vscode/mcp.json`). El token se pide al conectar y no queda en el archivo:
```json
{
  "inputs": [{ "id": "typst-token", "type": "promptString", "description": "Token de Typst Extend", "password": true }],
  "servers": {
    "typst-extend": {
      "type": "http",
      "url": "http://localhost:8080/mcp",
      "headers": { "Authorization": "Bearer ${input:typst-token}" }
    }
  }
}
```

**MCP Inspector**, para probar las herramientas a mano: ejecuta `npx @modelcontextprotocol/inspector`. Elige el transporte *Streamable HTTP* con la URL `http://localhost:8080/mcp` y añade la cabecera `Authorization: Bearer tse_…`, o usa su flujo OAuth.

**Desde un script** (JSON-RPC directo):
```bash
curl -s http://localhost:8080/mcp \
  -H "Authorization: Bearer tse_…" \
  -H "Content-Type: application/json" -H "Accept: application/json, text/event-stream" \
  -d '{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"list_projects","arguments":{}}}'
```

#### 2b. Con OAuth (conectores remotos)

Con `PUBLIC_URL` en https:

- **claude.ai y Claude Desktop:** abre *Configuración › Conectores › Añadir conector personalizado*, pon un nombre (p. ej. «Typst Extend») y la URL `https://…/mcp`, y deja vacíos el *Client ID* y el *Client Secret*. Pulsa «Conectar». En planes de equipo o empresa puede que solo un administrador de la organización pueda añadirlo.
- **ChatGPT:** activa *Configuración › Conectores › Avanzado › Modo desarrollador*. Luego, en *Conectores › Crear*, pon la URL `https://…/mcp` y la autenticación *OAuth*, y confirma que confías en el conector.
- **Claude Code con OAuth**, sin token: `claude mcp add --transport http typst-extend https://…/mcp`. Después, dentro de Claude Code, ejecuta `/mcp` › *typst-extend* › *Authenticate*. Funciona también con `http://localhost:8080/mcp`, porque se aceptan las redirecciones a `localhost`.

En todos los casos el cliente se registra solo (registro dinámico, RFC 7591) y abre en el navegador la pantalla **Autorizar aplicación** de Typst Extend. Si no tienes sesión, primero pide el usuario y la contraseña y luego vuelve a esa pantalla. Ahí eliges:
- **Permisos:** solo lectura, o lectura y escritura.
- **Proyectos:** todos tus proyectos (también los que se creen después) o solo los que marques.

Al autorizar vuelves a la aplicación, que queda conectada. Renueva el acceso sola (tokens de 1 h con refresco de 30 días); si dejas de usarla más de 30 días, habrá que autorizarla otra vez.

#### Problemas frecuentes

- **El cliente dice que no puede conectar o que falta la autorización:** revisa con `curl -i https://…/mcp` que responde `401` y que `https://…/.well-known/oauth-authorization-server` devuelve JSON con tu `PUBLIC_URL` como `issuer`. Si sale `localhost` o `http://`, `PUBLIC_URL` no está bien puesta o el servidor no se ha reiniciado.
- **`invalid_target` o «recurso no válido» al autorizar:** la URL del conector no coincide con `PUBLIC_URL/mcp` (otro host, otro puerto, `http` en lugar de `https`).
- **«Proyecto no encontrado»:** el acceso está limitado a otros proyectos, o el usuario no es propietario ni miembro del proyecto. Un administrador tampoco ve por MCP los proyectos de otros. Cambia los proyectos en **Agentes** › «Permisos».
- **«Este acceso es de solo lectura»:** el token o la aplicación no tiene `typst:write`. Edítalo en **Agentes**; con OAuth también puedes revocarlo y volver a conectar.
- **La aplicación dejó de funcionar de repente:** se revocó en **Agentes**, el usuario se desactivó, o se reutilizó un token de refresco (por seguridad eso revoca el acceso entero). Vuelve a conectar.

**Permisos.** Cada llamada se comprueba de nuevo:
1. El agente actúa como el usuario: solo ve sus proyectos y los compartidos con él. Si el usuario se desactiva o pierde el acceso a un proyecto, el agente también lo pierde al instante.
2. Por MCP, el rol de administrador no da acceso a los proyectos de otros.
3. Cada acceso (aplicación autorizada o token personal) puede limitarse a ciertos proyectos. Los proyectos que cree el agente se añaden a la lista.
4. Hay dos permisos: `typst:read` (leer, compilar, ver páginas, descargar) y `typst:write` (además crear proyectos y crear, editar, subir, renombrar y borrar archivos).

Los accesos se revisan, editan y revocan en **Agentes**. Los tokens se guardan solo como hash SHA-256. Los de acceso OAuth duran 1 h y los de refresco 30 días. Los de refresco rotan con cada uso, y reutilizar uno revoca todo el acceso.

**Herramientas**

| Herramienta | Permiso | Qué hace |
|---|---|---|
| `whoami` | lectura | usuario y alcance del acceso |
| `list_projects`, `get_project`, `list_templates` | lectura | proyectos accesibles (con quién los está editando) y plantillas |
| `create_project` | escritura | vacío o como copia de una plantilla o de otro proyecto |
| `update_project` | escritura | nombre o archivo principal (solo el propietario) |
| `list_files`, `read_file`, `search_files` | lectura | archivos, lectura por líneas numeradas, búsqueda (texto o regex) |
| `write_file`, `edit_file`, `insert_text` | escritura | crear/reescribir, reemplazo exacto de un fragmento, insertar en una línea |
| `rename_file`, `delete_file` | escritura | mover o borrar (no el archivo principal) |
| `upload_file`, `create_upload_url`, `upload_from_url` | escritura | subir en base64, con un enlace de un solo uso (`curl -T`) o desde una URL https pública |
| `download_file`, `view_image`, `export_project` | lectura | contenido o enlace de descarga (10 min), ver una imagen, `.zip` del proyecto |
| `compile` | lectura | compila y devuelve páginas y errores/advertencias con archivo:línea |
| `render_pages` | lectura | páginas como PNG (hasta 4 por llamada) |
| `page_source` | lectura | qué archivo y línea generan cada bloque de una página |
| `get_pdf` | lectura | enlace al PDF (o el PDF incrustado) |
| `git_status` | lectura | estado git local (solo el propietario); pull y push se hacen desde la web |

## API

Todas las rutas van bajo `/api` y usan `Authorization: Bearer <token>`. La excepción es el WebSocket, que recibe el token en `?token=`. En cada petición el backend vuelve a leer el usuario, así que desactivarlo o cambiar su rol surte efecto de inmediato.

| Método | Ruta | Acceso / notas |
|---|---|---|
| POST | `/auth/login` | público → `{token, expires_at, user}` |
| GET | `/auth/me` | sesión |
| GET · POST | `/users` | admin |
| PUT | `/users/{id}` | admin (nombre, rol, activo, contraseña) |
| GET | `/users/search?q=` | sesión; usuarios activos para invitar (máx. 10) |
| GET · POST | `/projects` | GET: destacados primero, con `member_names` y `editors`. POST `{name, git_remote?, git_branch?, github_account_id?}` (con remoto, lo clona) |
| GET · PUT · DELETE | `/projects/{id}` | con acceso; PUT/DELETE solo propietario o admin |
| POST | `/projects/{id}/clone` | con acceso o si es plantilla; `{name}` |
| GET | `/projects/{id}/thumbnail` | con acceso o si es plantilla; PNG |
| GET | `/templates` | sesión; todas las plantillas |
| PUT | `/projects/{id}/prefs` | con acceso; `{starred?, folder_id?}` (0 = sin carpeta) |
| GET · POST | `/projects/{id}/members` | GET con acceso; POST `{username}` propietario o admin |
| DELETE | `/projects/{id}/members/{userId}` | propietario o admin; un miembro puede quitarse a sí mismo |
| GET · POST · DELETE | `/projects/{id}/files` | con acceso (`?path=` en DELETE) |
| POST | `/projects/{id}/upload` | con acceso; multipart `file` (máx. 20 MB), `dir`, `path` (reemplazar un archivo existente), `optimize=1` (imágenes sin pérdida) |
| POST | `/projects/{id}/images/optimize` | con acceso; `{path}` → optimiza sin pérdida una imagen PNG/JPEG |
| GET | `/projects/{id}/raw?path=` | con acceso |
| GET | `/projects/{id}/preview` | con acceso → `{ok, rev, pages[{hash, width, height}], diagnostics}` |
| GET | `/projects/{id}/preview/sync` | con acceso → `{rev, points[{page, y, file, line, col}]}` (vista previa ↔ código) |
| GET | `/projects/{id}/preview/pages/{hash}` | con acceso → SVG de una página (gzip; inmutable, se guarda en caché) |
| GET | `/projects/{id}/pdf` | con acceso → PDF (422 con diagnósticos si falla) |
| GET | `/projects/{id}/git/status` | con acceso |
| POST | `/projects/{id}/git/pull` · `/git/push` | con acceso (`{message}` en push) |
| GET (WS) | `/projects/{id}/ws?path=&since=&token=` | con acceso; edición colaborativa |
| GET · POST | `/folders` | carpetas propias; POST `{name, color}` |
| PUT · DELETE | `/folders/{id}` | dueño; colores: blue, green, amber, violet, pink, cyan, orange, slate |
| GET · POST | `/github/accounts` | cuentas propias; POST `{login, token, label?}` valida el token |
| PUT · DELETE | `/github/accounts/{id}` | dueño (PUT `{label?, token?}`) |
| GET | `/github/accounts/{id}/repos` | dueño; `?refresh=1` ignora la caché de 2 min |
| GET | `/github/accounts/{id}/branches?repo=owner/name` | dueño |
| GET | `/health` | público |
| GET · POST | `/mcp/grants` · `/mcp/tokens` | sesión; accesos MCP propios / crear token personal `{name, scopes, all_projects, projects, expires_days}` |
| PUT · DELETE | `/mcp/grants/{id}` | dueño; `{scopes, all_projects, projects}` |
| GET · POST | `/oauth/authorize/info` · `/oauth/authorize` | sesión; pantalla de consentimiento OAuth |

Fuera de `/api`: `POST /mcp` (MCP, token en `Authorization`), `/.well-known/oauth-protected-resource[/mcp]`, `/.well-known/oauth-authorization-server`, `/oauth/register`, `/oauth/authorize`, `/oauth/token` y `/oauth/revoke`.

## Detrás de un proxy inverso

El servidor de Go sirve la app, la API y el MCP en un solo puerto (ver [Producción](#producción)). Si lo pones detrás de nginx, Caddy, etc.:
- reenvía todo (`/`) al backend, permitiendo la actualización a WebSocket (`Upgrade` / `Connection`) en `/api`;
- usa HTTPS: el frontend se conecta por `wss://` cuando la página se sirve por HTTPS, y los conectores MCP remotos lo exigen;
- pon `PUBLIC_URL` con el dominio público (`https://typst.midominio.com`);
- en `/mcp` desactiva el buffering de respuestas (`proxy_buffering off` en nginx), permite cuerpos de hasta 32 MB y usa un tiempo de espera de unos 10 min, porque compilar y renderizar puede tardar.

Ejemplo de nginx:
```nginx
location / {
    proxy_pass http://127.0.0.1:8080;
    proxy_http_version 1.1;
    proxy_set_header Upgrade $http_upgrade;
    proxy_set_header Connection $connection_upgrade;  # map $http_upgrade $connection_upgrade {default upgrade; '' close;}
    proxy_set_header Host $host;
    client_max_body_size 32m;
}
location /mcp {
    proxy_pass http://127.0.0.1:8080;
    proxy_http_version 1.1;
    proxy_buffering off;
    proxy_read_timeout 600s;
    client_max_body_size 32m;
}
```

## Seguridad

- Los tokens de GitHub se guardan sin cifrar en la base de datos. Protege el acceso a ella y a sus copias de seguridad.
- `backend/.env` contiene secretos y está excluido del repositorio.
- Los archivos subidos o traídos de GitHub no pueden salir del directorio del proyecto: se rechazan rutas con `..`, archivos ocultos y enlaces simbólicos, y git se ejecuta con `core.symlinks=false` y sin hooks.
