package config

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

// Setting es un valor suelto a guardar: sección, clave y el valor nuevo.
type Setting struct {
	Section string
	Key     string
	Value   any
}

// Save escribe los valores en el config.toml editando las líneas que cambian.
//
// Un volcado de la estructura entera borraría los comentarios que explican cada
// valor la primera vez que alguien toca un radio button, así que en vez de eso
// se reemplaza la línea de cada clave si ya está (conservando su comentario al
// margen), se agrega al final de su sección si falta, y se crea la sección si
// tampoco existe.
func Save(path string, values []Setting) (string, error) {
	if path == "" {
		path = ConfigPath()
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return path, err
	}
	if _, err := os.Stat(path); os.IsNotExist(err) {
		if err := os.WriteFile(path, []byte(Template), 0o644); err != nil {
			return path, err
		}
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return path, err
	}
	lines := splitLines(string(raw))
	for _, v := range values {
		lines = writeOne(lines, v.Section, v.Key, v.Value)
	}
	out := strings.Join(lines, "\n") + "\n"
	return path, os.WriteFile(path, []byte(out), 0o644)
}

// Init deja el config.toml de ejemplo en su lugar. Con force lo sobrescribe.
func Init(force bool) (string, error) {
	path := ConfigPath()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return path, err
	}
	if _, err := os.Stat(path); err == nil && !force {
		return path, nil
	}
	return path, os.WriteFile(path, []byte(Template), 0o644)
}

func splitLines(text string) []string {
	text = strings.TrimSuffix(text, "\n")
	if text == "" {
		return nil
	}
	return strings.Split(text, "\n")
}

var headerRe = regexp.MustCompile(`^\s*\[([^\]]+)\]\s*$`)

// assignmentRe arma el patrón de `clave = valor  # comentario` para una clave.
func assignmentRe(key string) *regexp.Regexp {
	return regexp.MustCompile(
		`^(\s*` + regexp.QuoteMeta(key) + `\s*=\s*)` +
			`("(?:[^"\\]|\\.)*"|'[^']*'|[^#\n]*?)` +
			`(\s*#.*)?$`)
}

// tomlValue serializa un valor al literal TOML que le corresponde.
func tomlValue(value any) string {
	switch v := value.(type) {
	case bool:
		if v {
			return "true"
		}
		return "false"
	case int:
		return strconv.Itoa(v)
	case int64:
		return strconv.FormatInt(v, 10)
	case float64:
		// Sin notación científica y sin decimales de más: el archivo lo lee gente.
		return strconv.FormatFloat(v, 'f', -1, 64)
	case string:
		esc := strings.ReplaceAll(v, `\`, `\\`)
		esc = strings.ReplaceAll(esc, `"`, `\"`)
		return `"` + esc + `"`
	default:
		return `"` + fmt.Sprint(v) + `"`
	}
}

func writeOne(lines []string, section, key string, value any) []string {
	rendered := key + " = " + tomlValue(value)
	pattern := assignmentRe(key)
	inSection := false
	endOfSection := -1 // última línea con contenido de la sección, + 1

	for i, line := range lines {
		if header := headerRe.FindStringSubmatch(line); header != nil {
			if inSection {
				break
			}
			inSection = strings.TrimSpace(header[1]) == section
			if inSection {
				endOfSection = i + 1
			}
			continue
		}
		if !inSection {
			continue
		}
		if m := pattern.FindStringSubmatch(line); m != nil {
			lines[i] = rendered + m[3]
			return lines
		}
		if strings.TrimSpace(line) != "" {
			endOfSection = i + 1
		}
	}

	if endOfSection < 0 { // la sección no estaba
		return append(lines, "", "["+section+"]", rendered)
	}
	out := make([]string, 0, len(lines)+1)
	out = append(out, lines[:endOfSection]...)
	out = append(out, rendered)
	out = append(out, lines[endOfSection:]...)
	return out
}
