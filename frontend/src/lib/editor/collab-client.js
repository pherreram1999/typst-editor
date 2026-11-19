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
  '&': { height: '100%', fontSize: '14px', backgroundColor: 'white' },
  '.cm-scroller': { fontFamily: "'JetBrains Mono', ui-monospace, SFMono-Regular, Menlo, monospace", lineHeight: '1.6' },
  '.cm-content': { paddingTop: '12px', paddingBottom: '40vh' },
  '.cm-gutters': { backgroundColor: '#F8FAFC', borderRight: '1px solid #E2E8F0', color: '#94A3B8' },
  '.cm-activeLineGutter': { backgroundColor: '#EFF6FF', color: '#1E40AF' },
  '.cm-activeLine': { backgroundColor: '#F8FAFF' },
  '&.cm-focused': { outline: 'none' },
  '.cm-selectionBackground, &.cm-focused .cm-selectionBackground': { backgroundColor: '#DBEAFE !important' },
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
      EditorView.updateListener.of((u) => {
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

  goto(line, column = 1) {
    if (!this.view) return
    const doc = this.view.state.doc
    const l = doc.line(Math.min(Math.max(line, 1), doc.lines))
    const pos = Math.min(l.from + Math.max(column - 1, 0), l.to)
    this.view.dispatch({ selection: { anchor: pos }, scrollIntoView: true })
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
