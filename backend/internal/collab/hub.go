package collab

import (
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"
	"unicode/utf8"
)

const (
	maxHistory = 2000            // actualizaciones guardadas para reanudar tras una reconexión
	MaxDocSize = 5 * 1024 * 1024 // unidades UTF-16
	saveDelay  = 800 * time.Millisecond
)

var (
	ErrNotText  = errors.New("el archivo no es texto UTF-8 editable")
	ErrTooLarge = errors.New("el documento supera el tamaño máximo")
)

// Update es una actualización tal como la produce/consume @codemirror/collab.
type Update struct {
	ClientID string          `json:"clientID"`
	Changes  json.RawMessage `json:"changes"`
}

// Peer es una conexión WebSocket de un usuario a un documento.
type Peer struct {
	ID     string
	UserID int64
	Name   string
	Color  string
	Send   chan []byte // mensajes salientes; lo consume el escritor de la conexión
	Closed chan struct{}
	once   sync.Once
	doc    *Doc
}

func NewPeer(id string, userID int64, name, color string) *Peer {
	return &Peer{ID: id, UserID: userID, Name: name, Color: color,
		Send: make(chan []byte, 256), Closed: make(chan struct{})}
}

// Kick cierra la conexión del peer (p. ej. si no consume mensajes a tiempo).
func (p *Peer) Kick() { p.once.Do(func() { close(p.Closed) }) }

func (p *Peer) send(msg []byte) {
	select {
	case p.Send <- msg:
	default:
		p.Kick() // cliente demasiado lento: se desconecta y reanudará
	}
}

// Doc es un archivo abierto por al menos un peer.
type Doc struct {
	hub       *Hub
	projectID int64
	path      string // relativo al proyecto, con "/"
	abs       string

	mu      sync.Mutex
	text    []uint16
	base    int // versión de updates[0]
	updates []Update
	peers   map[*Peer]struct{}
	dirty   bool
	timer   *time.Timer
	closed  bool // descargado de memoria
	deleted bool // archivo borrado: no volver a escribirlo
}

func (d *Doc) version() int { return d.base + len(d.updates) }

// Hub gestiona los documentos abiertos de todos los proyectos.
type Hub struct {
	projectDir func(projectID int64) string

	mu   sync.Mutex
	docs map[string]*Doc
	revs map[int64]int64 // revisión por proyecto: cambia con cualquier modificación
}

func NewHub(projectDir func(int64) string) *Hub {
	return &Hub{projectDir: projectDir, docs: map[string]*Doc{}, revs: map[int64]int64{}}
}

func key(projectID int64, path string) string { return fmt.Sprintf("%d:%s", projectID, path) }

// Rev devuelve la revisión actual del proyecto.
func (h *Hub) Rev(projectID int64) int64 {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.revs[projectID]
}

// BumpRev marca el proyecto como modificado y avisa a todos sus peers.
func (h *Hub) BumpRev(projectID int64, files bool) { h.bumpRev(projectID, files, "") }

// bumpRev es BumpRev con la ruta del documento editado, si el cambio fue
// solo una edición de texto: la vista previa del navegador ya tiene el texto
// del archivo que está abierto y solo descarga los demás.
func (h *Hub) bumpRev(projectID int64, files bool, path string) {
	h.mu.Lock()
	h.revs[projectID]++
	rev := h.revs[projectID]
	h.mu.Unlock()
	msg := map[string]any{"type": "rev", "rev": rev, "files": files}
	if path != "" {
		msg["path"] = path
	}
	h.broadcastProject(projectID, nil, mustJSON(msg))
}

// LoadText lee y valida un archivo de texto editable.
func LoadText(abs string) ([]uint16, error) {
	b, err := os.ReadFile(abs)
	if err != nil {
		return nil, err
	}
	if !utf8.Valid(b) {
		return nil, ErrNotText
	}
	t := encode(string(b))
	if len(t) > MaxDocSize {
		return nil, ErrTooLarge
	}
	return t, nil
}

// Join conecta un peer a un documento. Si since >= 0 y está dentro del
// historial, el peer reanuda desde esa versión (conserva sus cambios locales
// pendientes); si no, recibe el documento completo.
func (h *Hub) Join(projectID int64, path, abs string, p *Peer, since int) error {
	h.mu.Lock()
	d := h.docs[key(projectID, path)]
	if d == nil {
		text, err := LoadText(abs)
		if err != nil {
			h.mu.Unlock()
			return err
		}
		d = &Doc{hub: h, projectID: projectID, path: path, abs: abs, text: text, peers: map[*Peer]struct{}{}}
		h.docs[key(projectID, path)] = d
	}
	d.mu.Lock()
	h.mu.Unlock()
	defer d.mu.Unlock()

	p.doc = d
	d.peers[p] = struct{}{}
	if since >= d.base && since <= d.version() && since >= 0 {
		p.send(mustJSON(map[string]any{"type": "resume", "version": since}))
		if since < d.version() {
			p.send(mustJSON(map[string]any{"type": "updates", "version": since, "updates": d.updates[since-d.base:]}))
		}
	} else {
		p.send(mustJSON(map[string]any{"type": "init", "version": d.version(), "doc": decode(d.text)}))
	}
	go h.sendPresence(projectID)
	return nil
}

// Leave desconecta al peer; si el documento queda sin peers se guarda y se descarga.
func (h *Hub) Leave(p *Peer) {
	d := p.doc
	if d == nil {
		return
	}
	h.mu.Lock()
	d.mu.Lock()
	delete(d.peers, p)
	empty := len(d.peers) == 0
	if empty && h.docs[key(d.projectID, d.path)] == d {
		delete(h.docs, key(d.projectID, d.path))
		d.closed = true
		// Guardar antes de soltar el lock del hub: un Join posterior leerá el disco.
		d.saveLocked()
	}
	d.mu.Unlock()
	h.mu.Unlock()

	if !empty {
		d.broadcast(p, mustJSON(map[string]any{"type": "cursor-leave", "peer": p.ID}))
	}
	h.sendPresence(d.projectID)
}

// Push procesa las actualizaciones de un peer basadas en la versión indicada.
// Si la versión no es la actual se rechaza: el peer ya tiene (o recibirá antes
// que la respuesta) las actualizaciones que le faltan, hará rebase y reintentará.
func (h *Hub) Push(p *Peer, version int, updates []Update) error {
	d := p.doc
	d.mu.Lock()
	if d.closed || d.deleted {
		d.mu.Unlock()
		return errors.New("documento cerrado")
	}
	if version != d.version() {
		d.mu.Unlock()
		p.send(mustJSON(map[string]any{"type": "pushed", "ok": false}))
		return nil
	}
	text := d.text
	for _, u := range updates {
		var err error
		if text, err = applyChangeSet(text, u.Changes); err != nil {
			d.mu.Unlock()
			return err
		}
		if len(text) > MaxDocSize {
			d.mu.Unlock()
			return ErrTooLarge
		}
	}
	if len(updates) == 0 {
		d.mu.Unlock()
		p.send(mustJSON(map[string]any{"type": "pushed", "ok": true}))
		return nil
	}
	d.text = text
	d.appendLocked(updates)
	msg := mustJSON(map[string]any{"type": "updates", "version": version, "updates": updates})
	for q := range d.peers {
		q.send(msg)
	}
	p.send(mustJSON(map[string]any{"type": "pushed", "ok": true}))
	d.scheduleSaveLocked()
	d.mu.Unlock()

	h.bumpRev(d.projectID, false, d.path)
	return nil
}

func (d *Doc) appendLocked(updates []Update) {
	d.updates = append(d.updates, updates...)
	if over := len(d.updates) - maxHistory; over > 0 {
		d.updates = append([]Update(nil), d.updates[over:]...)
		d.base += over
	}
}

// Cursor retransmite la selección de un peer al resto de peers del documento.
func (h *Hub) Cursor(p *Peer, version, anchor, head int) {
	p.doc.broadcast(p, mustJSON(map[string]any{
		"type": "cursor", "peer": p.ID, "name": p.Name, "color": p.Color,
		"version": version, "anchor": anchor, "head": head,
	}))
}

func (d *Doc) broadcast(except *Peer, msg []byte) {
	d.mu.Lock()
	defer d.mu.Unlock()
	for q := range d.peers {
		if q != except {
			q.send(msg)
		}
	}
}

func (d *Doc) scheduleSaveLocked() {
	d.dirty = true
	if d.timer == nil {
		d.timer = time.AfterFunc(saveDelay, d.save)
	} else {
		d.timer.Reset(saveDelay)
	}
}

// save escribe el documento en disco si tiene cambios (escritura atómica).
func (d *Doc) save() {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.saveLocked()
}

func (d *Doc) saveLocked() {
	if !d.dirty || d.deleted {
		return
	}
	if err := writeAtomic(d.abs, []byte(decode(d.text))); err != nil {
		log.Printf("guardar %s: %v", d.abs, err)
		return
	}
	d.dirty = false
}

func writeAtomic(path string, data []byte) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), ".tmp-*")
	if err != nil {
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		os.Remove(tmp.Name())
		return err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmp.Name())
		return err
	}
	_ = os.Chmod(tmp.Name(), 0o644)
	return os.Rename(tmp.Name(), path)
}

func (h *Hub) projectDocs(projectID int64) []*Doc {
	h.mu.Lock()
	defer h.mu.Unlock()
	var out []*Doc
	for _, d := range h.docs {
		if d.projectID == projectID {
			out = append(out, d)
		}
	}
	return out
}

// FlushProject guarda en disco todos los documentos abiertos del proyecto.
func (h *Hub) FlushProject(projectID int64) {
	for _, d := range h.projectDocs(projectID) {
		d.save()
	}
}

// FlushAll guarda todos los documentos abiertos (al apagar el servidor).
func (h *Hub) FlushAll() {
	h.mu.Lock()
	docs := make([]*Doc, 0, len(h.docs))
	for _, d := range h.docs {
		docs = append(docs, d)
	}
	h.mu.Unlock()
	for _, d := range docs {
		d.save()
	}
}

// OpenPaths devuelve las rutas abiertas del proyecto.
func (h *Hub) OpenPaths(projectID int64) []string {
	var out []string
	for _, d := range h.projectDocs(projectID) {
		out = append(out, d.path)
	}
	return out
}

// ReplaceText sustituye el contenido de un documento abierto (p. ej. tras un
// git pull) emitiendo una actualización del servidor, de modo que los clientes
// la reciben como un cambio remoto más y transforman sus cambios pendientes.
func (h *Hub) ReplaceText(projectID int64, path, content string) {
	h.mu.Lock()
	d := h.docs[key(projectID, path)]
	h.mu.Unlock()
	if d == nil {
		return
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	next := encode(content)
	cs := diffChangeSet(d.text, next)
	if cs == nil {
		return
	}
	u := []Update{{ClientID: "server", Changes: cs}}
	v := d.version()
	d.text = next
	d.appendLocked(u)
	msg := mustJSON(map[string]any{"type": "updates", "version": v, "updates": u})
	for q := range d.peers {
		q.send(msg)
	}
}

// CloseDoc cierra un documento (archivo borrado) notificando a sus peers.
func (h *Hub) CloseDoc(projectID int64, path, reason string) {
	h.mu.Lock()
	d := h.docs[key(projectID, path)]
	delete(h.docs, key(projectID, path))
	h.mu.Unlock()
	if d == nil {
		return
	}
	d.mu.Lock()
	d.closed = true
	d.deleted = true
	if d.timer != nil {
		d.timer.Stop()
	}
	peers := d.peers
	d.mu.Unlock()
	msg := mustJSON(map[string]any{"type": "closed", "reason": reason})
	for q := range peers {
		q.send(msg)
		q.Kick()
	}
}

// CloseProject cierra todos los documentos del proyecto sin guardarlos.
func (h *Hub) CloseProject(projectID int64, reason string) {
	for _, d := range h.projectDocs(projectID) {
		h.CloseDoc(projectID, d.path, reason)
	}
}

// KickUser cierra las sesiones de un usuario en un proyecto (acceso revocado).
func (h *Hub) KickUser(projectID, userID int64, reason string) {
	msg := mustJSON(map[string]any{"type": "closed", "reason": reason, "revoked": true})
	for _, d := range h.projectDocs(projectID) {
		d.mu.Lock()
		for p := range d.peers {
			if p.UserID == userID {
				p.send(msg)
				p.Kick()
			}
		}
		d.mu.Unlock()
	}
}

// NotifyMembers avisa a los conectados de que cambió la lista de miembros.
func (h *Hub) NotifyMembers(projectID int64) {
	h.broadcastProject(projectID, nil, mustJSON(map[string]any{"type": "members"}))
}

// ActiveEditors devuelve, por proyecto, los nombres de los usuarios conectados
// (uno por usuario), sin incluir a exceptUserID.
func (h *Hub) ActiveEditors(exceptUserID int64) map[int64][]string {
	h.mu.Lock()
	docs := make([]*Doc, 0, len(h.docs))
	for _, d := range h.docs {
		docs = append(docs, d)
	}
	h.mu.Unlock()
	seen := map[int64]map[int64]bool{}
	out := map[int64][]string{}
	for _, d := range docs {
		d.mu.Lock()
		for p := range d.peers {
			if p.UserID == exceptUserID {
				continue
			}
			if seen[d.projectID] == nil {
				seen[d.projectID] = map[int64]bool{}
			}
			if !seen[d.projectID][p.UserID] {
				seen[d.projectID][p.UserID] = true
				out[d.projectID] = append(out[d.projectID], p.Name)
			}
		}
		d.mu.Unlock()
	}
	for _, names := range out {
		sort.Strings(names)
	}
	return out
}

type presence struct {
	ID     string `json:"id"`
	UserID int64  `json:"user_id"`
	Name   string `json:"name"`
	Color  string `json:"color"`
	Path   string `json:"path"`
}

func (h *Hub) sendPresence(projectID int64) {
	var list []presence
	for _, d := range h.projectDocs(projectID) {
		d.mu.Lock()
		for p := range d.peers {
			list = append(list, presence{p.ID, p.UserID, p.Name, p.Color, d.path})
		}
		d.mu.Unlock()
	}
	sort.Slice(list, func(i, j int) bool { return list[i].Name < list[j].Name })
	if list == nil {
		list = []presence{}
	}
	h.broadcastProject(projectID, nil, mustJSON(map[string]any{"type": "presence", "users": list}))
}

func (h *Hub) broadcastProject(projectID int64, except *Peer, msg []byte) {
	for _, d := range h.projectDocs(projectID) {
		d.broadcast(except, msg)
	}
}

func mustJSON(v any) []byte {
	b, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return b
}
