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
import { EditorState, ChangeSet } from '@codemirror/state'
import { EditorView, keymap } from '@codemirror/view'
import { basicSetup } from 'codemirror'
import { indentWithTab } from '@codemirror/commands'
import { syntaxHighlighting } from '@codemirror/language'
import { collab, receiveUpdates, sendableUpdates, getSyncedVersion } from '@codemirror/collab'
import { typstLanguage, typstHighlight } from './typst-lang.js'
import { remoteCursors, setRemoteCursor, removeRemoteCursor } from './cursors.js'
import { wsURL } from '../api.js'

const editorTheme = EditorView.theme({
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
   * @param {(files: File[], at: {pos: number}) => void} [o.onFilesDrop] archivos soltados en el editor (en vez de
   *   insertar su contenido); `at` es la posición donde se soltaron, que se mantiene al día con las ediciones
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
    this.connect()
  }

  extensions(version) {
    return [
      basicSetup,
      keymap.of([indentWithTab]),
      typstLanguage,
      syntaxHighlighting(typstHighlight),
      EditorView.lineWrapping,
      editorTheme,
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
        if (u.docChanged || u.selectionSet) this.scheduleCursor()
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
    }
    ws.onmessage = (ev) => this.handle(JSON.parse(ev.data))
    ws.onclose = (ev) => {
      if (this.ws !== ws) return
      this.ws = null
      if (this.destroyed || this.closedByServer) return
      // Un cambio rechazado por el servidor (1008) obliga a recargar desde cero.
      if (ev.code === 1008) {
        this.forceInit = true
        if (ev.reason) this.onWarning?.(ev.reason)
      }
      this.onStatus?.('offline')
      const delay = Math.min(10000, 500 * 2 ** this.retry++)
      this.reconnectTimer = setTimeout(() => this.connect(), delay)
    }
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
        this.scheduleCursor()
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

  get cursor() {
    return this.view ? this.view.state.selection.main.head : 0
  }

  get text() {
    return this.view ? this.view.state.doc.toString() : ''
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
    this.ws?.close()
    this.ws = null
    this.view?.destroy()
    this.view = null
  }
}
