// Cliente de edición colaborativa: CodeMirror 6 + @codemirror/collab sobre
// WebSocket.
//
// Algoritmo (OT con autoridad central):
//  - El servidor mantiene una secuencia lineal de actualizaciones (versión N).
//  - El cliente envía sus cambios locales pendientes junto con la versión en la
//    que se basan. El servidor solo los acepta si esa versión es la actual.
//  - Al recibir actualizaciones de otros, `receiveUpdates` transforma (rebase)
//    los cambios locales no confirmados sobre ellas, de modo que todos los
//    clientes convergen al mismo documento sin conflictos.
//  - Si un envío se rechaza, el cliente ya habrá recibido antes los cambios que
//    le faltaban (el servidor los manda por la misma conexión, en orden), hace
//    rebase y reintenta.
import { EditorState, ChangeSet, Prec, Compartment } from '@codemirror/state'
import { EditorView, keymap } from '@codemirror/view'
import { basicSetup } from 'codemirror'
import { indentWithTab } from '@codemirror/commands'
import { syntaxHighlighting } from '@codemirror/language'
import { collab, receiveUpdates, sendableUpdates, getSyncedVersion } from '@codemirror/collab'
import { typstLanguage, typstHighlight } from './typst-lang.js'
import { bibLanguage } from './bib-lang.js'
import { remoteCursors, setRemoteCursor, removeRemoteCursor } from './cursors.js'
import { wsURL } from '../api.js'
import { formatKeymap } from './commands.js'
import { imageHover, imageHoverTheme } from './imageHover.js'
import { tableHover, tableHoverTheme } from './tableHover.js'
import { marginGuide, savedMargin } from './marginGuide.js'
import { spellcheck, spellable } from './spellcheck.js'
import { spellEnabled } from '../spell/speller.js'

export const editorTheme = EditorView.theme({
  // Colores de app.css: cambian con el tema.
  '&': { height: '100%', fontSize: '14px', backgroundColor: 'var(--color-surface)', color: 'var(--color-slate-900)' },
  '.cm-scroller': { fontFamily: "'JetBrains Mono', ui-monospace, SFMono-Regular, Menlo, monospace", lineHeight: '1.6' },
  '.cm-content': { paddingTop: '12px', paddingBottom: '40vh' },
  '.cm-gutters': { backgroundColor: 'var(--color-slate-50)', borderRight: '1px solid var(--color-slate-200)', color: 'var(--color-slate-400)' },
  '.cm-activeLineGutter': { backgroundColor: 'var(--color-primary-tint)', color: 'var(--color-primary)' },
  '.cm-activeLine': { backgroundColor: 'var(--editor-active-line)' },
  '&.cm-focused': { outline: 'none' },
  '.cm-cursor, .cm-dropCursor': { borderLeftColor: 'var(--color-slate-900)' },
  '.cm-selectionBackground, &.cm-focused .cm-selectionBackground': { backgroundColor: 'var(--editor-selection) !important' },
  '.cm-selectionMatch': { backgroundColor: 'var(--color-amber-100)' },
  '&.cm-focused .cm-matchingBracket': { backgroundColor: 'var(--color-emerald-100)', outline: 'none' },
  '.cm-tooltip, .cm-panels': { backgroundColor: 'var(--color-surface)', color: 'var(--color-slate-900)', borderColor: 'var(--color-slate-200)' },
  '.cm-tooltip-autocomplete > ul > li[aria-selected]': { backgroundColor: 'var(--color-primary-soft)', color: 'var(--color-slate-900)' },
  '.cm-panels input, .cm-panels button': { color: 'inherit' },
  '.cm-searchMatch': { backgroundColor: 'var(--color-amber-200)' },
  '.cm-foldPlaceholder': { backgroundColor: 'var(--color-slate-100)', borderColor: 'var(--color-slate-300)', color: 'var(--color-slate-600)' },
})

// Latido: cada HEARTBEAT_EVERY se envía un ping; si en HEARTBEAT_TIMEOUT no
// llega ningún mensaje, la conexión se da por muerta y se reconecta.
const HEARTBEAT_EVERY = 20000
const HEARTBEAT_TIMEOUT = 8000
// Reintentos: 250 ms, 500 ms, 1 s, 2 s y luego cada ~3 s (con algo de azar
// para que los clientes no lleguen todos a la vez tras reiniciar el servidor).
const backoff = (n) => Math.min(3000, 250 * 2 ** n) * (0.8 + Math.random() * 0.4)

// Los navegadores llaman «image.png» a toda imagen pegada: se le da un nombre único con la
// fecha para no sobrescribir otra captura (ni chocar con la detección de duplicados de Uppy).
let pasteSeq = 0
function pastedName(f) {
  if (f.name && !/^image\.\w+$/i.test(f.name)) return f
  const ext = (f.type.split('/')[1] || 'png').replace('jpeg', 'jpg').replace(/\+.*/, '')
  const d = new Date()
  const p = (n) => String(n).padStart(2, '0')
  const stamp = `${d.getFullYear()}${p(d.getMonth() + 1)}${p(d.getDate())}-${p(d.getHours())}${p(d.getMinutes())}${p(d.getSeconds())}`
  return new File([f], `captura-${stamp}-${++pasteSeq}.${ext}`, { type: f.type, lastModified: Date.now() })
}

export class CollabSession {
  /**
   * @param {object} o
   * @param {number} o.projectId
   * @param {string} o.path
   * @param {HTMLElement} o.parent
   * @param {(s: 'connecting'|'online'|'offline'|'closed', detail?: string) => void} [o.onStatus]
   * @param {(msg: object) => void} [o.onMessage] presencia, revisiones…
   * @param {() => void} [o.onSynced] cambios confirmados o recibidos
   * @param {(msg: string) => void} [o.onWarning]
   * @param {(pos: {line: number, column: number}) => void} [o.onCursorClick] clic que deja el cursor (sin selección)
   * @param {(state: EditorState) => void} [o.onState] estado tras cada cambio o movimiento del cursor (barra de herramientas)
   * @param {boolean} [o.readOnly] solo lectura: no se aceptan cambios del usuario ni se envían al servidor
   * @param {() => void} [o.onImage] atajo «insertar imagen»
   * @param {() => void} [o.onCite] atajo «insertar cita»
   * @param {(ctx: import('@codemirror/autocomplete').CompletionContext) => any} [o.completeCite] autocompletado tras «@» (solo .typ)
   * @param {object} [o.imageHandlers] tarjeta sobre image("…"): { resolve, onEdit } (ver imageHover.js)
   * @param {object} [o.tableHandlers] tarjeta sobre #table(…): { onEdit({ from, to }) } (ver tableHover.js)
   * @param {() => void} [o.onDocChange] el texto cambió (edición local o remota, o documento recibido); ver text y loaded
   * @param {(files: File[], at: {pos: number}) => void} [o.onFilesDrop] archivos soltados o imágenes pegadas en el
   *   editor (en vez de insertar su contenido); `at` es la posición donde se soltaron (o el cursor al pegar), que se
   *   mantiene al día con las ediciones
   */
  constructor(o) {
    Object.assign(this, o)
    this.view = null
    this.ws = null
    this.inflight = false
    this.destroyed = false
    this.retry = 0
    this.forceInit = false
    this.cursorTimer = null
    this.tracked = new Set() // posiciones que se actualizan con cada cambio (track)
    this.marginCol = savedMargin() // guía de margen derecho (0: sin guía)
    this.marginConf = new Compartment()
    this.spellOn = spellEnabled() // revisión ortográfica (por navegador)
    this.spellConf = new Compartment()
    this.lastMsg = 0
    // Al volver a la pestaña, recuperar la red o el foco: reconectar ya en vez
    // de esperar al temporizador (Chrome lo retrasa en pestañas ocultas) y
    // comprobar que una conexión «abierta» sigue viva (suspensión, cambio de red).
    this.wake = () => {
      if (document.visibilityState === 'hidden') return
      if (this.ws) this.probe(5000)
      else this.reconnectNow()
    }
    window.addEventListener('online', this.wake)
    window.addEventListener('focus', this.wake)
    document.addEventListener('visibilitychange', this.wake)
    this.heartbeat = setInterval(() => this.probe(HEARTBEAT_TIMEOUT), HEARTBEAT_EVERY)
    this.connect()
  }

  extensions(version) {
    return [
      basicSetup,
      keymap.of([indentWithTab]),
      // Atajos de formato por encima de los de basicSetup (p. ej. Mod-i, Mod-u).
      // Solo lectura (invitado con enlace de lectura): sin atajos de formato y
      // ningún cambio local; los remotos (receiveUpdates usa filter: false) sí llegan.
      this.readOnly
        ? [EditorState.readOnly.of(true), EditorState.changeFilter.of(() => false)]
        : Prec.high(keymap.of(formatKeymap({ image: () => this.onImage?.(), cite: () => this.onCite?.() }))),
      this.imageHandlers ? [imageHover(this.imageHandlers), EditorView.theme(imageHoverTheme)] : [],
      this.tableHandlers && !this.isBib ? [tableHover(this.tableHandlers), EditorView.theme(tableHoverTheme)] : [],
      this.isBib ? bibLanguage : typstLanguage,
      // Fuente de autocompletado adicional (la de basicSetup consulta languageData).
      !this.isBib && this.completeCite ? EditorState.languageData.of(() => [{ autocomplete: this.completeCite }]) : [],
      syntaxHighlighting(typstHighlight),
      EditorView.lineWrapping,
      editorTheme,
      this.marginConf.of(marginGuide(this.marginCol)),
      this.spellConf.of(this.spellExtension()),
      remoteCursors,
      collab({ startVersion: version }),
      EditorView.domEventHandlers({
        dragover: (e) => {
          if (this.onFilesDrop && [...(e.dataTransfer?.types ?? [])].includes('Files')) {
            e.preventDefault()
            e.dataTransfer.dropEffect = 'copy'
          }
          return false
        },
        drop: (e, view) => {
          const files = [...(e.dataTransfer?.files ?? [])]
          if (!files.length || !this.onFilesDrop) return false
          e.preventDefault()
          const pos = view.posAtCoords({ x: e.clientX, y: e.clientY }) ?? view.state.selection.main.head
          this.onFilesDrop(files, this.track(pos))
          return true
        },
        // Imágenes pegadas (captura de pantalla, «Copiar imagen»): misma subida que al soltarlas,
        // insertadas en el cursor. Si el portapapeles también trae texto (celdas de Excel, párrafos
        // de Word, que además vienen como imagen) se pega el texto, como espera el usuario.
        paste: (e, view) => {
          if (!this.onFilesDrop) return false
          const cd = e.clipboardData
          const images = [...(cd?.files ?? [])].filter((f) => f.type.startsWith('image/'))
          if (!images.length || cd.getData('text/plain').trim()) return false
          e.preventDefault()
          this.onFilesDrop(images.map(pastedName), this.track(view.state.selection.main.head))
          return true
        },
        mouseup: (e, view) => {
          if (e.button !== 0 || !this.onCursorClick) return
          // Después de que CodeMirror coloque el cursor.
          setTimeout(() => {
            const sel = view.state.selection.main
            if (!sel.empty) return
            const line = view.state.doc.lineAt(sel.head)
            this.onCursorClick({ line: line.number, column: sel.head - line.from + 1 })
          })
        },
      }),
      EditorView.updateListener.of((u) => {
        if (u.docChanged) for (const t of this.tracked) t.pos = u.changes.mapPos(t.pos)
        if (u.docChanged) this.push()
        if (u.docChanged) this.onDocChange?.()
        if (u.docChanged || u.selectionSet) this.scheduleCursor()
        if (u.docChanged || u.selectionSet) this.notifyState()
      }),
    ]
  }

  connect() {
    if (this.destroyed) return
    this.onStatus?.('connecting')
    const since = this.view && !this.forceInit ? getSyncedVersion(this.view.state) : null
    const ws = new WebSocket(wsURL(this.projectId, this.path, since))
    this.ws = ws
    this.inflight = false
    ws.onopen = () => {
      this.retry = 0
      this.lastMsg = Date.now()
    }
    ws.onmessage = (ev) => {
      this.lastMsg = Date.now()
      this.handle(JSON.parse(ev.data))
    }
    ws.onclose = (ev) => this.dropped(ws, ev.code, ev.reason)
  }

  // La conexión ws terminó (cierre normal o dada por muerta): reintentar.
  dropped(ws, code, reason) {
    if (this.ws !== ws) return
    this.ws = null
    clearTimeout(this.probeTimer)
    if (this.destroyed || this.closedByServer) return
    // Un cambio rechazado por el servidor (1008) obliga a recargar desde cero.
    if (code === 1008) {
      this.forceInit = true
      if (reason) this.onWarning?.(reason)
    }
    this.onStatus?.('offline')
    clearTimeout(this.reconnectTimer)
    this.reconnectTimer = setTimeout(() => this.connect(), backoff(this.retry++))
  }

  // Reconectar sin esperar al siguiente reintento.
  reconnectNow() {
    if (this.destroyed || this.closedByServer || this.ws) return
    clearTimeout(this.reconnectTimer)
    this.retry = 0
    this.connect()
  }

  // Comprueba que la conexión responde: si en `timeout` ms no llega ningún
  // mensaje (el servidor contesta al ping), se abandona y se reconecta. Sin
  // esto, una conexión muerta (suspensión, cambio de red) puede tardar minutos
  // en cerrarse y el editor seguiría diciendo «Sincronizado».
  probe(timeout) {
    const ws = this.ws
    if (!ws || ws.readyState !== WebSocket.OPEN || this.probeTimer) return
    const sent = Date.now()
    this.send({ type: 'ping' })
    this.probeTimer = setTimeout(() => {
      this.probeTimer = null
      if (this.ws !== ws || this.lastMsg >= sent) return
      ws.onclose = null
      ws.close()
      this.dropped(ws, 1006, '')
    }, timeout)
  }

  send(msg) {
    if (this.ws?.readyState === WebSocket.OPEN) this.ws.send(JSON.stringify(msg))
  }

  handle(msg) {
    switch (msg.type) {
      case 'init': {
        const state = EditorState.create({ doc: msg.doc, extensions: this.extensions(msg.version) })
        if (!this.view) {
          this.view = new EditorView({ state, parent: this.parent })
        } else {
          if (sendableUpdates(this.view.state).length) {
            this.onWarning?.('Se perdió la conexión demasiado tiempo: se recargó el documento y algunos cambios locales no se pudieron enviar.')
          }
          this.view.setState(state)
        }
        this.forceInit = false
        this.onStatus?.('online')
        this.onSynced?.()
        this.onDocChange?.()
        this.scheduleCursor()
        this.notifyState()
        break
      }
      case 'resume':
        this.onStatus?.('online')
        this.push()
        break
      case 'updates': {
        if (!this.view) return
        const v = getSyncedVersion(this.view.state)
        let updates = msg.updates
        if (msg.version > v) {
          // Hueco en la secuencia (no debería ocurrir): reconectar.
          this.ws?.close()
          return
        }
        updates = updates.slice(v - msg.version)
        if (!updates.length) return
        this.view.dispatch(
          receiveUpdates(
            this.view.state,
            updates.map((u) => ({ changes: ChangeSet.fromJSON(u.changes), clientID: u.clientID })),
          ),
        )
        this.onSynced?.()
        break
      }
      case 'pushed':
        this.inflight = false
        this.push()
        this.sendCursor()
        break
      case 'cursor': {
        if (!this.view || msg.version !== getSyncedVersion(this.view.state)) return
        // Las posiciones vienen en el documento confirmado: mapearlas a través de
        // los cambios locales todavía pendientes.
        let { anchor, head } = msg
        for (const u of sendableUpdates(this.view.state)) {
          anchor = u.changes.mapPos(anchor)
          head = u.changes.mapPos(head)
        }
        this.view.dispatch({ effects: setRemoteCursor.of({ ...msg, anchor, head }) })
        break
      }
      case 'pong': // latido: basta con haber recibido algo (lastMsg)
        break
      case 'cursor-leave':
        this.view?.dispatch({ effects: removeRemoteCursor.of(msg.peer) })
        break
      case 'closed':
        this.closedByServer = true
        this.onStatus?.('closed', msg.reason, msg.revoked)
        break
      default:
        this.onMessage?.(msg)
    }
  }

  push() {
    if (this.readOnly) return
    if (!this.view || this.inflight || this.ws?.readyState !== WebSocket.OPEN) return
    const updates = sendableUpdates(this.view.state)
    if (!updates.length) return
    this.inflight = true
    this.send({
      type: 'push',
      version: getSyncedVersion(this.view.state),
      updates: updates.map((u) => ({ clientID: u.clientID, changes: u.changes.toJSON() })),
    })
  }

  scheduleCursor() {
    clearTimeout(this.cursorTimer)
    this.cursorTimer = setTimeout(() => this.sendCursor(), 80)
  }

  // Solo se envía la selección cuando no hay cambios pendientes, para que sus
  // posiciones correspondan a la versión confirmada.
  sendCursor() {
    if (!this.view || sendableUpdates(this.view.state).length) return
    const sel = this.view.state.selection.main
    this.send({ type: 'cursor', version: getSyncedVersion(this.view.state), anchor: sel.anchor, head: sel.head })
  }

  // Posición que se desplaza con las ediciones (propias o remotas) hasta
  // que se libera con untrack.
  track(pos) {
    const t = { pos }
    this.tracked.add(t)
    return t
  }

  untrack(t) {
    this.tracked.delete(t)
  }

  // Avisa del estado (una vez por fotograma) para la barra de herramientas.
  notifyState() {
    if (!this.onState || this.stateFrame) return
    this.stateFrame = requestAnimationFrame(() => {
      this.stateFrame = 0
      if (this.view) this.onState(this.view.state)
    })
  }

  get isBib() {
    return /\.bib$/i.test(this.path)
  }

  // Cambios hechos desde fuera del editor (editor visual de .bib): sin mover
  // el cursor ni el foco; entran en el historial (Ctrl+Z) como cualquier otro.
  applyChanges(changes) {
    if (!this.view || !changes.length) return false
    this.view.dispatch({ changes, userEvent: 'input.visual' })
    return true
  }

  // Reemplaza [from, to) y deja el cursor al final.
  replaceRange(from, to, text) {
    if (!this.view) return false
    this.view.dispatch({ changes: { from, to, insert: text }, selection: { anchor: from + text.length }, scrollIntoView: true })
    this.view.focus()
    return true
  }

  get cursor() {
    return this.view ? this.view.state.selection.main.head : 0
  }

  get text() {
    return this.view ? this.view.state.doc.toString() : ''
  }

  // Ya se recibió el documento (antes, text está vacío aunque no lo esté).
  get loaded() {
    return !!this.view
  }

  // Inserta un bloque (p. ej. una figura) separado por líneas en blanco:
  // si pos cae en un párrafo, después de ese párrafo (en Typst un bloque
  // pegado al texto quedaría dentro de él); si cae en una línea vacía, ahí.
  // Selecciona `select` (posiciones relativas al texto) para seguir escribiendo.
  insertBlock(pos, text, select) {
    if (!this.view) return false
    const doc = this.view.state.doc
    let line = doc.lineAt(Math.min(Math.max(pos, 0), doc.length))
    const blank = (l) => l.text.trim() === ''
    let at, before, after
    if (!blank(line)) {
      while (line.number < doc.lines && !blank(doc.line(line.number + 1))) line = doc.line(line.number + 1)
      at = line.to
      before = '\n\n'
      after = line.number < doc.lines ? '' : '\n' // la línea siguiente ya está vacía
    } else {
      at = line.from
      before = line.number > 1 && !blank(doc.line(line.number - 1)) ? '\n' : ''
      // Una línea en blanco antes del siguiente párrafo; al final del archivo, salto final.
      after = line.number === doc.lines || !blank(doc.line(line.number + 1)) ? '\n' : ''
    }
    const start = at + before.length
    this.view.dispatch({
      changes: { from: at, insert: before + text + after },
      selection: { anchor: start + select[0], head: start + select[1] },
      scrollIntoView: true,
    })
    this.view.focus()
    return true
  }

  // Revisión ortográfica: en .typ/.txt/.md, si está activada y se puede editar.
  spellExtension() {
    return this.spellOn && !this.readOnly && spellable(this.path) ? spellcheck() : []
  }

  setSpell(on) {
    this.spellOn = on
    this.view?.dispatch({ effects: this.spellConf.reconfigure(this.spellExtension()) })
  }

  // Cambia la columna de la guía de margen (0: quitarla).
  setMargin(col) {
    this.marginCol = col
    this.view?.dispatch({ effects: this.marginConf.reconfigure(marginGuide(col)) })
  }

  goto(line, column = 1) {
    if (!this.view) return
    const doc = this.view.state.doc
    const l = doc.line(Math.min(Math.max(line, 1), doc.lines))
    const pos = Math.min(l.from + Math.max(column - 1, 0), l.to)
    // La línea de destino queda arriba del editor (con un pequeño margen),
    // no en el borde más cercano.
    this.view.dispatch({ selection: { anchor: pos }, effects: EditorView.scrollIntoView(pos, { y: 'start', yMargin: 24 }) })
    this.view.focus()
  }

  get pending() {
    return this.view ? sendableUpdates(this.view.state).length > 0 : false
  }

  destroy() {
    this.destroyed = true
    clearTimeout(this.reconnectTimer)
    clearTimeout(this.cursorTimer)
    clearTimeout(this.probeTimer)
    clearInterval(this.heartbeat)
    window.removeEventListener('online', this.wake)
    window.removeEventListener('focus', this.wake)
    document.removeEventListener('visibilitychange', this.wake)
    this.ws?.close()
    this.ws = null
    this.view?.destroy()
    this.view = null
  }
}
