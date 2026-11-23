package httpapi

import (
	"errors"
	"log"
	"net/http"
)

// apiError es un error con el código HTTP con el que responderlo. Lo usan las
// funciones compartidas por los handlers HTTP y las herramientas MCP: los
// primeros lo traducen a {"error": msg} con su código y las segundas a un
// resultado de herramienta con isError.
type apiError struct {
	status int
	msg    string
	logged bool // la causa ya se registró (internalErr)
}

func (e *apiError) Error() string { return e.msg }

func errf(status int, msg string) error { return &apiError{status: status, msg: msg} }

// internalErr registra la causa real (con archivo:línea) y devuelve un 500
// «error interno» que no expone detalles al cliente ni al agente MCP.
func internalErr(cause error) error {
	log.Printf("error interno: %v [%s]", cause, caller(2))
	return &apiError{status: http.StatusInternalServerError, msg: "error interno", logged: true}
}

// writeAPIError responde un error devuelto por una función compartida.
func writeAPIError(w http.ResponseWriter, err error) {
	var ae *apiError
	if errors.As(err, &ae) {
		if ae.status >= 500 && !ae.logged {
			log.Printf("error %d: %s [%s]", ae.status, ae.msg, caller(2))
		}
		writeJSON(w, ae.status, map[string]string{"error": ae.msg})
		return
	}
	log.Printf("error interno: %v [%s]", err, caller(2))
	writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "error interno"})
}
