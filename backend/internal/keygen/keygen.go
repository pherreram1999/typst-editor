// Package keygen genera el secreto JWT y lo escribe en el archivo .env.
package keygen

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"os"
	"strings"
)

const envKey = "JWT_SECRET"

// NewSecret devuelve 32 bytes aleatorios en hexadecimal (64 caracteres).
func NewSecret() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

// WriteEnv asigna JWT_SECRET en envPath. Si envPath no existe, lo crea a partir
// de examplePath (si existe). Reemplaza la línea existente o la añade al final.
// Devuelve true si ya había un secreto no vacío (que fue reemplazado).
func WriteEnv(envPath, examplePath, secret string) (replaced bool, err error) {
	data, err := os.ReadFile(envPath)
	if errors.Is(err, os.ErrNotExist) {
		data, err = os.ReadFile(examplePath)
		if errors.Is(err, os.ErrNotExist) {
			data, err = nil, nil
		}
	}
	if err != nil {
		return false, err
	}

	lines := strings.Split(string(data), "\n")
	found := false
	for i, line := range lines {
		k, v, ok := strings.Cut(strings.TrimSpace(line), "=")
		if !ok || strings.TrimSpace(k) != envKey {
			continue
		}
		replaced = strings.Trim(strings.TrimSpace(v), `"'`) != ""
		lines[i] = envKey + "=" + secret
		found = true
		break
	}
	if !found {
		if n := len(lines); n > 0 && lines[n-1] == "" {
			lines = lines[:n-1]
		}
		lines = append(lines, envKey+"="+secret, "")
	}
	// 0600: el .env contiene secretos.
	return replaced, os.WriteFile(envPath, []byte(strings.Join(lines, "\n")), 0o600)
}
