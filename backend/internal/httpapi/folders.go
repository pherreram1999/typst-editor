package httpapi

import (
	"database/sql"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/go-sql-driver/mysql"
)

// Folder es una carpeta personal para organizar proyectos. Cada usuario tiene
// las suyas; un proyecto está como mucho en una carpeta de cada usuario.
type Folder struct {
	ID    int64  `json:"id"`
	Name  string `json:"name"`
	Color string `json:"color"`
	Count int    `json:"count"` // proyectos accesibles dentro
}

// Colores de carpeta: claves de una paleta fija que define el frontend.
var folderColors = map[string]bool{"blue": true, "green": true, "amber": true, "violet": true,
	"pink": true, "cyan": true, "orange": true, "slate": true}

func folderName(raw string) (string, error) {
	n := strings.Join(strings.Fields(raw), " ")
	if n == "" || utf8.RuneCountInString(n) > 64 {
		return "", errors.New("nombre de carpeta requerido (máx. 64 caracteres)")
	}
	return n, nil
}

func (s *Server) loadFolder(w http.ResponseWriter, r *http.Request) (int64, bool) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "id inválido")
		return 0, false
	}
	var owner int64
	err = s.db.QueryRowContext(r.Context(), `SELECT user_id FROM project_folders WHERE id = ?`, id).Scan(&owner)
	if errors.Is(err, sql.ErrNoRows) || (err == nil && owner != currentUser(r).ID) {
		writeError(w, http.StatusNotFound, "carpeta no encontrada")
		return 0, false
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "error interno")
		return 0, false
	}
	return id, true
}

func (s *Server) listFolders(w http.ResponseWriter, r *http.Request) {
	u := currentUser(r)
	// Solo cuentan los proyectos a los que el usuario sigue teniendo acceso.
	rows, err := s.db.QueryContext(r.Context(), `
SELECT f.id, f.name, f.color,
  (SELECT COUNT(*) FROM project_user_prefs pr
   JOIN projects p ON p.id = pr.project_id
   LEFT JOIN project_members m ON m.project_id = p.id AND m.user_id = pr.user_id
   WHERE pr.folder_id = f.id AND `+projectAccess+`)
FROM project_folders f WHERE f.user_id = ? ORDER BY f.name`, u.ID, u.Role, u.ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "error interno")
		return
	}
	defer rows.Close()
	out := []Folder{}
	for rows.Next() {
		var f Folder
		if err := rows.Scan(&f.ID, &f.Name, &f.Color, &f.Count); err != nil {
			writeError(w, http.StatusInternalServerError, "error interno")
			return
		}
		out = append(out, f)
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) saveFolder(w http.ResponseWriter, r *http.Request, id int64) {
	var in struct {
		Name  *string `json:"name"`
		Color *string `json:"color"`
	}
	if err := decode(r, &in); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	f := Folder{ID: id, Color: "blue"}
	if id != 0 {
		if err := s.db.QueryRowContext(r.Context(), `SELECT name, color FROM project_folders WHERE id = ?`, id).Scan(&f.Name, &f.Color); err != nil {
			writeError(w, http.StatusInternalServerError, "error interno")
			return
		}
	}
	var err error
	if in.Name != nil || id == 0 {
		raw := ""
		if in.Name != nil {
			raw = *in.Name
		}
		if f.Name, err = folderName(raw); err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
	}
	if in.Color != nil {
		if !folderColors[*in.Color] {
			writeError(w, http.StatusBadRequest, "color de carpeta inválido")
			return
		}
		f.Color = *in.Color
	}
	status := http.StatusOK
	if id == 0 {
		var res sql.Result
		res, err = s.db.ExecContext(r.Context(), `INSERT INTO project_folders (user_id, name, color) VALUES (?, ?, ?)`,
			currentUser(r).ID, f.Name, f.Color)
		if err == nil {
			f.ID, _ = res.LastInsertId()
			status = http.StatusCreated
		}
	} else {
		_, err = s.db.ExecContext(r.Context(), `UPDATE project_folders SET name = ?, color = ? WHERE id = ?`, f.Name, f.Color, id)
	}
	var me *mysql.MySQLError
	if errors.As(err, &me) && me.Number == 1062 {
		writeError(w, http.StatusConflict, "ya tienes una carpeta con ese nombre")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "error interno")
		return
	}
	writeJSON(w, status, f)
}

func (s *Server) createFolder(w http.ResponseWriter, r *http.Request) { s.saveFolder(w, r, 0) }

func (s *Server) renameFolder(w http.ResponseWriter, r *http.Request) {
	if id, ok := s.loadFolder(w, r); ok {
		s.saveFolder(w, r, id)
	}
}

// deleteFolder borra la carpeta; sus proyectos quedan sin carpeta (ON DELETE SET NULL).
func (s *Server) deleteFolder(w http.ResponseWriter, r *http.Request) {
	id, ok := s.loadFolder(w, r)
	if !ok {
		return
	}
	if _, err := s.db.ExecContext(r.Context(), `DELETE FROM project_folders WHERE id = ?`, id); err != nil {
		writeError(w, http.StatusInternalServerError, "error interno")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// updatePrefs destaca un proyecto o lo mueve de carpeta (solo para el usuario actual).
// PUT /api/projects/{id}/prefs {"starred": bool, "folder_id": id|0}
func (s *Server) updatePrefs(w http.ResponseWriter, r *http.Request) {
	p, ok := s.loadProject(w, r)
	if !ok {
		return
	}
	var in struct {
		Starred  *bool  `json:"starred"`
		FolderID *int64 `json:"folder_id"`
	}
	if err := decode(r, &in); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	u := currentUser(r)
	starred, folder := p.Starred, p.FolderID
	if in.Starred != nil {
		starred = *in.Starred
	}
	if in.FolderID != nil {
		if *in.FolderID == 0 {
			folder = nil
		} else {
			var owner int64
			err := s.db.QueryRowContext(r.Context(), `SELECT user_id FROM project_folders WHERE id = ?`, *in.FolderID).Scan(&owner)
			if err != nil || owner != u.ID {
				writeError(w, http.StatusBadRequest, "carpeta no válida")
				return
			}
			folder = in.FolderID
		}
	}
	// starred_at conserva la fecha original si ya estaba destacado.
	if _, err := s.db.ExecContext(r.Context(), `
INSERT INTO project_user_prefs (user_id, project_id, folder_id, starred, starred_at)
VALUES (?, ?, ?, ?, IF(?, CURRENT_TIMESTAMP, NULL))
ON DUPLICATE KEY UPDATE
  starred_at = IF(VALUES(starred), IF(starred, starred_at, CURRENT_TIMESTAMP), NULL),
  starred = VALUES(starred),
  folder_id = VALUES(folder_id)`, u.ID, p.ID, folder, starred, starred); err != nil {
		writeError(w, http.StatusInternalServerError, "error interno")
		return
	}
	p, err := s.projectByID(r, u, p.ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "error interno")
		return
	}
	writeJSON(w, http.StatusOK, p)
}
