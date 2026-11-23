package httpapi

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"strconv"
	"time"

	"github.com/coder/websocket"

	"typsteditor/backend/internal/collab"
)

var peerColors = []string{"#1E40AF", "#B91C1C", "#047857", "#7C3AED", "#C2410C", "#0E7490", "#BE185D", "#4D7C0F"}

var pongMsg = []byte(`{"type":"pong"}`)

type wsIn struct {
	Type    string          `json:"type"`
	Version int             `json:"version"`
	Updates []collab.Update `json:"updates"`
	Anchor  int             `json:"anchor"`
	Head    int             `json:"head"`
}

// projectWS abre la sesión de edición colaborativa de un archivo.
// GET /api/projects/{id}/ws?path=main.typ&since=<versión>&token=<jwt>
// (los navegadores no permiten cabeceras en WebSocket: el token va en la URL).
func (s *Server) projectWS(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	tok := q.Get("token")
	u, g, status, msg := s.authenticate(r.Context(), tok)
	if status != 0 {
		writeError(w, status, msg)
		return
	}
	if u.MustChangePassword {
		writeError(w, http.StatusForbidden, "debes cambiar tu contraseña temporal antes de continuar")
		return
	}
	ctx0 := context.WithValue(r.Context(), ctxKey{}, u) // para loadProject
	if g != nil {
		ctx0 = context.WithValue(ctx0, guestKey{}, g)
	}
	r = r.WithContext(ctx0)
	p, ok := s.loadProject(w, r)
	if !ok {
		return
	}
	rel, err := cleanPath(q.Get("path"))
	if err != nil || !isEditable(rel) {
		writeError(w, http.StatusBadRequest, "archivo no editable")
		return
	}
	abs, _, err := s.regularFile(p.ID, rel)
	if err != nil {
		writeError(w, http.StatusNotFound, "archivo no encontrado")
		return
	}
	since := -1
	if v, err := strconv.Atoi(q.Get("since")); err == nil {
		since = v
	}

	// Quitar los plazos del servidor HTTP: la conexión es de larga duración.
	rc := http.NewResponseController(w)
	_ = rc.SetReadDeadline(time.Time{})
	_ = rc.SetWriteDeadline(time.Time{})

	c, err := websocket.Accept(w, r, nil)
	if err != nil {
		return
	}
	defer c.CloseNow()
	c.SetReadLimit(4 * collab.MaxDocSize)

	var idb [8]byte
	_, _ = rand.Read(idb[:])
	color := peerColors[int(u.ID%int64(len(peerColors))+int64(len(peerColors)))%len(peerColors)]
	peer := collab.NewPeer(hex.EncodeToString(idb[:]), u.ID, u.Name, color)
	readOnly := g != nil && !g.CanEdit
	// Revalidación periódica: usuario activo o enlace de invitado vigente.
	valid := func(ctx context.Context) bool {
		if g != nil {
			_, err := s.guestFromToken(ctx, tok)
			return err == nil
		}
		u, err := s.userByID(ctx, u.ID)
		return err == nil && u.Active
	}
	if g != nil {
		peer.Invite = g.InviteID
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go s.wsWriter(ctx, c, peer, valid)

	if err := s.hub.Join(p.ID, rel, abs, peer, since); err != nil {
		msg := "no se pudo abrir el archivo"
		if errors.Is(err, collab.ErrNotText) || errors.Is(err, collab.ErrTooLarge) {
			msg = err.Error()
		}
		c.Close(websocket.StatusPolicyViolation, msg)
		return
	}
	defer s.hub.Leave(peer)

	for {
		_, data, err := c.Read(ctx)
		if err != nil {
			return
		}
		var m wsIn
		if err := json.Unmarshal(data, &m); err != nil {
			c.Close(websocket.StatusUnsupportedData, "mensaje inválido")
			return
		}
		switch m.Type {
		case "push":
			if readOnly {
				c.Close(websocket.StatusPolicyViolation, "este enlace de invitado es de solo lectura")
				return
			}
			if err := s.hub.Push(peer, m.Version, m.Updates); err != nil {
				log.Printf("ws push rechazado (proyecto %d, %s): %v", p.ID, rel, err)
				c.Close(websocket.StatusPolicyViolation, err.Error())
				return
			}
		case "cursor":
			s.hub.Cursor(peer, m.Version, m.Anchor, m.Head)
		case "ping":
			// Latido del cliente: le permite detectar conexiones muertas.
			select {
			case peer.Send <- pongMsg:
			default:
			}
		}
	}
}

// wsWriter envía los mensajes del peer, hace ping periódico y revalida que el
// usuario siga activo (o que el enlace del invitado siga vigente).
func (s *Server) wsWriter(ctx context.Context, c *websocket.Conn, p *collab.Peer, valid func(context.Context) bool) {
	ping := time.NewTicker(30 * time.Second)
	defer ping.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-p.Closed:
			// Entregar lo pendiente (p. ej. el aviso de cierre) antes de cerrar.
			for drained := false; !drained; {
				select {
				case msg := <-p.Send:
					wctx, cancel := context.WithTimeout(ctx, 2*time.Second)
					_ = c.Write(wctx, websocket.MessageText, msg)
					cancel()
				default:
					drained = true
				}
			}
			c.Close(websocket.StatusGoingAway, "desconectado")
			return
		case msg := <-p.Send:
			wctx, cancel := context.WithTimeout(ctx, 10*time.Second)
			err := c.Write(wctx, websocket.MessageText, msg)
			cancel()
			if err != nil {
				c.CloseNow()
				return
			}
		case <-ping.C:
			if !valid(ctx) {
				c.Close(websocket.StatusPolicyViolation, "sesión no válida")
				return
			}
			pctx, cancel := context.WithTimeout(ctx, 10*time.Second)
			err := c.Ping(pctx)
			cancel()
			if err != nil {
				c.CloseNow()
				return
			}
		}
	}
}
