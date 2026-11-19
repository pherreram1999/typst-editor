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
	uid, err := s.tokens.Parse(q.Get("token"))
	if err != nil {
		writeError(w, http.StatusUnauthorized, "token inválido o expirado")
		return
	}
	u, err := s.userByID(r.Context(), uid)
	if err != nil || !u.Active {
		writeError(w, http.StatusUnauthorized, "sesión no válida")
		return
	}
	r = r.WithContext(context.WithValue(r.Context(), ctxKey{}, u)) // para loadProject
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
	peer := collab.NewPeer(hex.EncodeToString(idb[:]), u.ID, u.Name, peerColors[int(u.ID)%len(peerColors)])

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go s.wsWriter(ctx, c, peer, u.ID)

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
			if err := s.hub.Push(peer, m.Version, m.Updates); err != nil {
				log.Printf("ws push rechazado (proyecto %d, %s): %v", p.ID, rel, err)
				c.Close(websocket.StatusPolicyViolation, err.Error())
				return
			}
		case "cursor":
			s.hub.Cursor(peer, m.Version, m.Anchor, m.Head)
		}
	}
}

// wsWriter envía los mensajes del peer, hace ping periódico y revalida que el
// usuario siga activo.
func (s *Server) wsWriter(ctx context.Context, c *websocket.Conn, p *collab.Peer, userID int64) {
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
			if u, err := s.userByID(ctx, userID); err != nil || !u.Active {
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
