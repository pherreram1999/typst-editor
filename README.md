# Typst Editor

Editor web colaborativo de documentos [Typst](https://typst.app): varias personas editan el mismo documento a la vez, ven la vista previa y el PDF al instante y sincronizan el proyecto con GitHub.

## Funcionalidades

- **Edición simultánea sin conflictos.** CodeMirror 6 + `@codemirror/collab` (Operational Transformation con autoridad central) sobre WebSocket, con cursores y presencia de los demás usuarios.
- **Vista previa en vivo y visor PDF**, generados con el binario `typst` del sistema. Los errores de compilación enlazan a la línea del código.
- **Subida de archivos con Uppy** (arrastrar y soltar, carpeta de destino), **reemplazo** de imágenes y **optimización sin pérdida** de PNG/JPEG: mismos píxeles y mismo tamaño en el documento, pero mucho menos peso (los diagramas suelen bajar a la cuarta parte).
- **Sincronización vista previa ↔ código:** un clic en la vista previa lleva al párrafo correspondiente del código y un clic en el código desplaza la vista previa hasta ese bloque y lo resalta.
- **Documentos grandes** (tesis de cientos de páginas con muchas capturas): la vista previa se compila con copias reducidas de las imágenes que conservan su tamaño en la página, así que la paginación es la misma que la del PDF (que usa los originales). El navegador solo descarga y muestra las páginas cercanas a la vista y, al recompilar, solo las que cambiaron. `typst` corre con prioridad baja y con un límite de procesos simultáneos.
- **Proyectos multiarchivo** (`.typ`, `.bib`, imágenes…). Cada proyecto es un repositorio git en `DATA_DIR/projects/<id>`.
- **Plantilla por defecto** en tamaño carta (`us-letter`) y español de México (`lang: "es", region: "mx"`).
- **Plantillas y copias.** Cualquier proyecto se puede duplicar. El propietario puede publicarlo como plantilla con categoría y descripción (p. ej. distintas carátulas). Las plantillas aparecen para todos en una galería con miniatura y se usan creando una copia.
- **Proyectos compartidos.** El propietario invita a usuarios registrados como colaboradores con permiso de edición.
- **Organización personal.** Cada usuario destaca proyectos (siempre aparecen primero) y los agrupa en carpetas con color. Nadie más ve esa organización.
- **Sincronización con GitHub.** Cada usuario registra sus cuentas (usuario + token). Al crear un proyecto puede buscar entre sus repositorios y cargar uno. *Pull* y *push* son manuales y usan el binario `git` del sistema.
- **Usuarios y roles** (`admin` / `user`) con autenticación JWT.

## Tecnologías

| Capa | Tecnología |
|---|---|
| Frontend | Svelte 5, Vite 8, Tailwind CSS 4, CodeMirror 6 |
| Backend | Go (`net/http`), WebSocket (`coder/websocket`), JWT HS256, bcrypt |
| Base de datos | MariaDB (probado con 11.8; las migraciones usan `ALTER TABLE … IF NOT EXISTS`) |
| Herramientas del sistema | `typst` (≥ 0.12), `git` |

Identidad visual: color principal blue-800 (`#1E40AF`), neutros slate y tipografía Inter. Los tokens están en `frontend/src/app.css`.

## Requisitos

- Go 1.27 o superior (ver `backend/go.mod`).
- Node.js en versión LTS reciente (probado con Node 26).
- MariaDB.
- `typst` y `git` en el `PATH`, o sus rutas en `TYPST_BIN` / `GIT_BIN`.

## Puesta en marcha

### 1. Base de datos

```bash
# Cambia antes la contraseña del usuario 'typsteditor' en el script
sudo mariadb < database/01_setup.sql
```

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

## Configuración (`backend/.env`)

| Variable | Por defecto | Descripción |
|---|---|---|
| `ADDR` | `:8080` | Dirección de escucha (API y frontend) |
| `DB_HOST`, `DB_PORT`, `DB_NAME`, `DB_USER`, `DB_PASSWORD` | `127.0.0.1`, `3306`, `typsteditor`, `typsteditor`, — | Conexión a MariaDB |
| `JWT_SECRET` | — | Obligatorio, mínimo 32 caracteres (`go run . key:generate`) |
| `TOKEN_TTL` | `8h` | Duración de la sesión |
| `ADMIN_USER`, `ADMIN_NAME`, `ADMIN_PASSWORD` | `admin`, `Administrador`, — | Primer administrador (solo si no existe ninguno) |
| `DATA_DIR` | `data` | Proyectos (`projects/<id>`, repos git) y compilaciones (`build/<id>`) |
| `TYPST_BIN`, `GIT_BIN` | `typst`, `git` | Binarios del sistema |
| `GITHUB_API` | `https://api.github.com` | Cámbiala para GitHub Enterprise (`https://<host>/api/v3`) |
| `STATIC_DIR` | `public/` junto al ejecutable, o `../frontend/dist` | Frontend compilado que sirve el propio servidor |

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

## Producción

El backend solo sirve la API. Para producción:
- compila el frontend (`npm run build`) y sirve `frontend/dist/` con tu servidor web;
- reenvía `/api` al backend, permitiendo la actualización a WebSocket (`Upgrade` / `Connection`);
- usa HTTPS: el frontend se conecta por `wss://` cuando la página se sirve por HTTPS.

## Seguridad

- Los tokens de GitHub se guardan sin cifrar en la base de datos. Protege el acceso a ella y a sus copias de seguridad.
- `backend/.env` contiene secretos y está excluido del repositorio.
- Los archivos subidos o traídos de GitHub no pueden salir del directorio del proyecto: se rechazan rutas con `..`, archivos ocultos y enlaces simbólicos, y git se ejecuta con `core.symlinks=false` y sin hooks.
