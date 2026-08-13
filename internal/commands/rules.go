package commands

import (
	"sort"
	"strings"
)

type opKind int

const (
	opNone opKind = iota
	opDeleteWord
	opDeleteAll
)

// rule es qué hace un comando: qué escribe, qué teclas manda, y cómo se pega a
// lo que tiene alrededor.
//
// El pegado es la mitad del trabajo. Una coma dictada tiene que quedar contra
// la palabra anterior y separada de la siguiente; un "¿" al revés; y una barra
// pegada de los dos lados, que es lo que hace que "carpeta barra archivo" dé
// una ruta y no tres palabras sueltas.
type rule struct {
	emit       string
	keys       []string
	tightLeft  bool
	tightRight bool
	op         opKind
}

// Los cuatro pegados posibles, para que la tabla se lea de un vistazo.
func closing(s string) rule { return rule{emit: s, tightLeft: true} }                   // ,  ;  ?
func opening(s string) rule { return rule{emit: s, tightRight: true} }                  // ¿  ¡  $
func glued(s string) rule   { return rule{emit: s, tightLeft: true, tightRight: true} } // /  _  *
func loose(s string) rule   { return rule{emit: s} }                                    // +  =  &

// wrap es "entre corchetes": escribe el par y retrocede un lugar, así lo que
// dictes después cae adentro.
func wrap(pair string) rule {
	return rule{emit: pair, keys: []string{"Left"}, tightRight: true}
}

// builtin son los comandos de fábrica indexados por la frase ya normalizada, y
// es contra este mapa que se hace el match. Se deriva de catalog en init().
var builtin = map[string]rule{}

// longestBuiltin es cuántas palabras tiene la frase más larga.
var longestBuiltin int

// catalog son los comandos de fábrica escritos como se dicen —con tilde y
// todo—, que es lo que se lista en `dictador commands`.
var catalog = map[string]rule{
	// Puntuación
	"punto y aparte":  {emit: ".", tightLeft: true, keys: []string{"Return"}},
	"punto y seguido": closing("."),
	"coma":            closing(","),
	"punto y coma":    closing(";"),

	// Interrogación y admiración, que en castellano abren y cierran
	"abre pregunta":          opening("¿"),
	"abre interrogación":     opening("¿"),
	"abre signo de pregunta": opening("¿"),
	"signo de pregunta":      closing("?"),
	"signo de interrogación": closing("?"),
	"cierra pregunta":        closing("?"),
	"cierra interrogación":   closing("?"),
	"abre admiración":        opening("¡"),
	"abre exclamación":       opening("¡"),
	"signo de admiración":    closing("!"),
	"signo de exclamación":   closing("!"),
	"cierra admiración":      closing("!"),
	"cierra exclamación":     closing("!"),

	// Teclas
	//
	// "espacio" solo no está, y no es un olvido: es una palabra que aparece en
	// cualquier charla ("el alfajor Capitán del Espacio"). Un comando tiene que
	// ser algo que sólo se dice cuando estás hablando de editar texto, y por eso
	// van "espacio espacio" y "signo mayor" pero no "espacio" ni "mayor".
	"enter":           {keys: []string{"Return"}},
	"tab":             {keys: []string{"Tab"}},
	"espacio espacio": glued("  "),

	// Símbolos que se pegan a lo que tienen al lado
	"guión":           glued("-"),
	"guión bajo":      glued("_"),
	"barra":           glued("/"),
	"barra invertida": glued("\\"),
	"asterisco":       glued("*"),

	// Símbolos que arrancan algo
	"signo pesos":    opening("$"),
	"signo de pesos": opening("$"),
	"numeral":        opening("#"),
	"hashtag":        opening("#"),

	// Operadores, que van sueltos como en la aritmética escrita
	"signo más":   loose("+"),
	"signo igual": loose("="),
	"ampersand":   loose("&"),
	"signo mayor": loose(">"),
	"signo menor": loose("<"),

	// Pares con el cursor adentro
	"entre corchetes":  wrap("[]"),
	"entre comillas":   wrap(`""`),
	"entre paréntesis": wrap("()"),

	// Borrados
	"borrar palabra": {op: opDeleteWord},
	"borrá palabra":  {op: opDeleteWord},
	"borrá eso":      {op: opDeleteAll},
	"borrar eso":     {op: opDeleteAll},
}

func init() {
	for phrase, r := range catalog {
		key := normalizePhrase(phrase)
		builtin[key] = r
		if n := len(strings.Fields(key)); n > longestBuiltin {
			longestBuiltin = n
		}
	}
}

// ruleSet mezcla los comandos de fábrica con los del config del usuario, y dice
// de cuántas palabras es la frase más larga que hay que probar.
//
// El usuario puede agregar los suyos, cambiar uno de fábrica, o apagarlo
// dejándolo en "".
func ruleSet(replacements map[string]string) (map[string]rule, int) {
	if len(replacements) == 0 {
		return builtin, longestBuiltin
	}
	rules := make(map[string]rule, len(builtin)+len(replacements))
	for k, v := range builtin {
		rules[k] = v
	}
	longest := longestBuiltin
	for phrase, emit := range replacements {
		key := normalizePhrase(phrase)
		if key == "" {
			continue
		}
		if emit == "" {
			delete(rules, key)
			continue
		}
		rules[key] = userRule(emit)
		if n := len(strings.Fields(key)); n > longest {
			longest = n
		}
	}
	return rules, longest
}

// closers y openers son los caracteres que deciden solos cómo se pega un
// reemplazo del usuario, sin obligarlo a explicarlo en el config.
const (
	closers = `,.;:!?)]}»%…`
	openers = `¿¡([{«$#@`
)

// userRule adivina el pegado de un reemplazo escrito en el config.
//
// Si el usuario puso los espacios a mano —"coma" = " —"— se respetan los suyos
// y no se agrega ninguno.
func userRule(emit string) rule {
	r := rule{emit: emit}
	first := []rune(emit)[0]
	last := []rune(emit)[len([]rune(emit))-1]
	r.tightLeft = first == ' ' || strings.ContainsRune(closers, first)
	r.tightRight = last == ' ' || strings.ContainsRune(openers, last)
	return r
}

// Command es un comando listo para mostrar: qué se dice y qué pasa.
type Command struct {
	Say    string   `json:"say"`
	Writes string   `json:"writes,omitempty"`
	Keys   []string `json:"keys,omitempty"`
	Note   string   `json:"note,omitempty"`
	Custom bool     `json:"custom,omitempty"`
}

// List son todos los comandos que van a andar con esta configuración, ordenados
// alfabéticamente y con los del usuario marcados.
func List(opts Options) []Command {
	out := make([]Command, 0, len(catalog)+len(opts.Replacements))
	silenced := map[string]bool{}
	for phrase, emit := range opts.Replacements {
		key := normalizePhrase(phrase)
		if key == "" {
			continue
		}
		silenced[key] = true // el built-in con esa frase queda pisado o apagado
		if emit == "" {
			continue
		}
		out = append(out, describe(phrase, userRule(emit), true))
	}
	for phrase, r := range catalog {
		if silenced[normalizePhrase(phrase)] {
			continue
		}
		out = append(out, describe(phrase, r, false))
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Say < out[j].Say })
	return out
}

func describe(phrase string, r rule, custom bool) Command {
	cmd := Command{Say: phrase, Writes: r.emit, Keys: r.keys, Custom: custom}
	switch r.op {
	case opDeleteWord:
		cmd.Note = "borra la última palabra dictada"
	case opDeleteAll:
		cmd.Note = "borra todo lo dictado hasta ahí"
	}
	if len(r.keys) == 1 && r.keys[0] == "Left" {
		cmd.Note = "y deja el cursor en el medio"
	}
	return cmd
}

// normalizePhrase normaliza una frase entera, palabra por palabra.
func normalizePhrase(phrase string) string {
	words := strings.Fields(phrase)
	for i, w := range words {
		words[i] = normalize(w)
	}
	return strings.Join(words, " ")
}
