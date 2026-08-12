// Package history guarda lo que se fue dictando, una línea JSON por dictado.
package history

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/neitanod/dictador/internal/config"
)

// Entry es un dictado que ya se entregó.
type Entry struct {
	At     string `json:"at"`
	Text   string `json:"text"`
	Action string `json:"action"`
	Target string `json:"target"`
}

// Path es el archivo donde vive el historial.
func Path() string { return filepath.Join(config.StateDir(), "history.jsonl") }

// Append agrega un dictado. Que falle no puede romper el dictado en curso, así
// que el error se devuelve para que quien llame decida ignorarlo.
func Append(entry Entry) error {
	if entry.At == "" {
		entry.At = time.Now().Format("2006-01-02T15:04:05")
	}
	if err := os.MkdirAll(config.StateDir(), 0o755); err != nil {
		return err
	}
	file, err := os.OpenFile(Path(), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	defer file.Close()

	line, err := json.Marshal(entry)
	if err != nil {
		return err
	}
	_, err = file.Write(append(line, '\n'))
	return err
}

// Load lee los últimos `limit` dictados (0 = todos).
func Load(limit int) ([]Entry, error) {
	raw, err := os.ReadFile(Path())
	if err != nil {
		return nil, err
	}
	var out []Entry
	for _, line := range strings.Split(string(raw), "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		var entry Entry
		if err := json.Unmarshal([]byte(line), &entry); err != nil {
			continue // una línea rota no puede tapar el resto del historial
		}
		out = append(out, entry)
	}
	if limit > 0 && len(out) > limit {
		out = out[len(out)-limit:]
	}
	return out, nil
}
