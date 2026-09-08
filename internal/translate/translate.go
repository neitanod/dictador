// Package translate es el catálogo de idiomas de la traducción instantánea y
// las reglas para leer la tabla de letras del config.
//
// Traducir lo dictado es una decisión que se toma en el último instante: la
// letra que tengas apretada cuando soltás la tecla del dictado elige el idioma,
// y si no hay ninguna el texto se pega como lo dijiste. Acá vive lo que esa
// decisión necesita saber —qué idiomas hay, cómo se llaman, qué letra apunta a
// cuál—, sin depender de X ni del motor que traduzca.
package translate

import (
	"sort"
	"strings"
	"unicode"
)

// Language es un idioma destino: el código que entiende el traductor y el
// nombre con el que se lo elige en la pantalla de configuración.
type Language struct {
	Code string `json:"code"`
	Name string `json:"name"`
}

// Languages son los idiomas que se ofrecen, en el orden en que se muestran: los
// que se usan desde acá arriba, y después el resto alfabético.
//
// El traductor de Google acepta bastantes más; el que quiera uno que no está
// puede escribir su código a mano en el config.toml y funciona igual.
var Languages = []Language{
	{"en", "Inglés"},
	{"pt", "Portugués"},
	{"es", "Castellano"},
	{"fr", "Francés"},
	{"it", "Italiano"},
	{"de", "Alemán"},
	{"ar", "Árabe"},
	{"ca", "Catalán"},
	{"zh-CN", "Chino (simplificado)"},
	{"zh-TW", "Chino (tradicional)"},
	{"ko", "Coreano"},
	{"da", "Danés"},
	{"he", "Hebreo"},
	{"hi", "Hindi"},
	{"nl", "Neerlandés"},
	{"el", "Griego"},
	{"id", "Indonesio"},
	{"ja", "Japonés"},
	{"no", "Noruego"},
	{"pl", "Polaco"},
	{"ro", "Rumano"},
	{"ru", "Ruso"},
	{"sv", "Sueco"},
	{"th", "Tailandés"},
	{"cs", "Checo"},
	{"tr", "Turco"},
	{"uk", "Ucraniano"},
	{"vi", "Vietnamita"},
	{"gl", "Gallego"},
	{"eu", "Euskera"},
	{"la", "Latín"},
}

// names indexa el catálogo por código, en minúsculas.
var names = func() map[string]string {
	out := make(map[string]string, len(Languages))
	for _, l := range Languages {
		out[strings.ToLower(l.Code)] = l.Name
	}
	return out
}()

// Name es cómo se llama un idioma en castellano, o el código pelado si es uno
// que no está en el catálogo: alguien lo escribió a mano y hay que respetarlo.
func Name(code string) string {
	code = strings.TrimSpace(code)
	if code == "" {
		return ""
	}
	if name, ok := names[strings.ToLower(code)]; ok {
		return name
	}
	// "en-GB" no está en el catálogo pero "en" sí, y decir "Inglés" es mejor
	// que devolver el código crudo.
	if base, _, cut := strings.Cut(code, "-"); cut {
		if name, ok := names[strings.ToLower(base)]; ok {
			return name
		}
	}
	return code
}

// NormalizeCode deja el código del idioma como lo espera el traductor.
//
// Los de región van con el país en mayúsculas ("zh-CN"), que es como los
// escribe Google, y el resto en minúsculas.
func NormalizeCode(code string) string {
	code = strings.TrimSpace(code)
	base, region, cut := strings.Cut(code, "-")
	if !cut {
		return strings.ToLower(code)
	}
	return strings.ToLower(base) + "-" + strings.ToUpper(region)
}

// NormalizeKey deja la letra como se guarda en la tabla: una sola, minúscula.
//
// Devuelve "" para lo que no puede ser una tecla de idioma —vacío, dos letras,
// un espacio—, que es la fila a medio llenar de la pantalla de configuración.
func NormalizeKey(key string) string {
	key = strings.TrimSpace(key)
	runes := []rune(key)
	if len(runes) != 1 {
		return ""
	}
	r := unicode.ToLower(runes[0])
	if unicode.IsSpace(r) || !unicode.IsPrint(r) {
		return ""
	}
	return string(r)
}

// Binding es una letra apuntando a un idioma, ya normalizada.
type Binding struct {
	Key      string `json:"key"`
	Language string `json:"language"`
}

// Bindings ordena y limpia la tabla del config para mostrarla o guardarla.
//
// Las filas sin letra o sin idioma se tiran sin decir nada: son la fila para
// agregar que quedó vacía.
func Bindings(keys map[string]string) []Binding {
	out := make([]Binding, 0, len(keys))
	for key, lang := range keys {
		key, lang = NormalizeKey(key), NormalizeCode(lang)
		if key == "" || lang == "" {
			continue
		}
		out = append(out, Binding{Key: key, Language: lang})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Key < out[j].Key })
	return out
}

// Describe es cómo se le cuenta al usuario a qué idioma va: "Inglés", con la
// letra que lo pidió cuando hace falta recordárselo.
func Describe(key, lang string) string {
	name := Name(lang)
	if key == "" {
		return name
	}
	return name + " (" + strings.ToUpper(key) + ")"
}
