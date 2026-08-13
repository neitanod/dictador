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
	return path, writeLines(path, lines)
}

// Pair es una entrada de una tabla del config: la clave tal como se dice y su
// valor. Se guardan en orden, que es el que se va a leer después en el archivo.
type Pair struct {
	Key   string
	Value string
}

// SaveTable reescribe una tabla entera —una sección con sus claves y nada
// más— dejando exactamente las entradas que se le pasan.
//
// [Save] no alcanza para esto: sabe cambiar y agregar claves, y no sabe sacar
// las que el usuario borró. Una tabla como [commands.replacements] es una
// lista, no un puñado de valores sueltos, y guardarla es escribirla entera.
//
// Los comentarios se quedan donde están. Los que están pegados abajo del
// header explican esa tabla y siguen ahí arriba; el bloque de comentarios que
// arranca después de un renglón en blanco al final es el que le presenta la
// sección que sigue, así que las entradas se meten antes y no lo empujan.
func SaveTable(path, section string, pairs []Pair) (string, error) {
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

	rendered := make([]string, 0, len(pairs))
	for _, p := range pairs {
		rendered = append(rendered, tomlValue(p.Key)+" = "+tomlValue(p.Value))
	}

	start, end := -1, -1 // el cuerpo de la sección, sin el header
	for i, line := range lines {
		header := headerRe.FindStringSubmatch(line)
		if header == nil {
			continue
		}
		if start >= 0 {
			end = i
			break
		}
		if strings.TrimSpace(header[1]) == section {
			start = i + 1
		}
	}
	if start < 0 { // la tabla no estaba
		out := append(append([]string{}, lines...), "", "["+section+"]")
		return path, writeLines(path, append(out, rendered...))
	}
	if end < 0 {
		end = len(lines)
	}

	var body []string
	for _, line := range lines[start:end] {
		if isComment(line) {
			body = append(body, line)
		}
	}
	cut := cutoff(body)
	head, tail := body[:cut], body[cut:]

	out := append([]string{}, lines[:start]...)
	out = append(out, head...)
	out = append(out, rendered...)
	out = append(out, tail...)
	return path, writeLines(path, append(out, lines[end:]...))
}

// isComment dice si un renglón no es una asignación: un comentario o un vacío.
func isComment(line string) bool {
	trimmed := strings.TrimSpace(line)
	return trimmed == "" || strings.HasPrefix(trimmed, "#")
}

// cutoff es dónde empieza el bloque final de comentarios que ya no habla de
// esta tabla: el último renglón en blanco que no tiene nada más que
// comentarios detrás.
func cutoff(body []string) int {
	for i := len(body) - 1; i >= 0; i-- {
		if strings.TrimSpace(body[i]) == "" {
			return i
		}
	}
	return len(body)
}

func writeLines(path string, lines []string) error {
	return os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o644)
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
	case []string:
		items := make([]any, len(v))
		for i, s := range v {
			items[i] = s
		}
		return tomlValue(items)
	case []any:
		parts := make([]string, len(v))
		for i, item := range v {
			parts[i] = tomlValue(item)
		}
		return "[" + strings.Join(parts, ", ") + "]"
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
