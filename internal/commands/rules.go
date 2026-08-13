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
	//
	// "punto final" y "palabra coma" son las dos maneras largas de pedir lo
	// mismo que "punto y seguido" y "coma". Están porque el que dicta las dice:
	// "coma" es una palabra de todos los días y "punto" también, así que cuando
	// uno quiere el signo y no la palabra tiende a alargar la frase para que se
	// entienda. Que las dos formas anden es más barato que acordarse de cuál es
	// la que este programa espera.
	"punto y aparte":  {emit: ".", tightLeft: true, keys: []string{"Return"}},
	"punto y seguido": closing("."),
	"punto final":     closing("."),
	"coma":            closing(","),
	"palabra coma":    closing(","),
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

// Entry es una fila del editor de comandos: la frase que se dice, lo que
// escribe hoy, y de dónde salió ese "hoy".
//
// List alcanza para mostrar los comandos, y no para editarlos: el que edita
// necesita saber además qué escribía de fábrica el que tocó —para poder
// volver atrás— y cuáles no se pueden escribir en el config, que son los que
// mandan teclas o borran.
type Entry struct {
	// Key es la frase normalizada, que es con la que se matchea y la única
	// forma de saber que "guión" del catálogo y "guion" del config son el
	// mismo comando.
	Key    string   `json:"key"`
	Say    string   `json:"say"`
	Writes string   `json:"writes"`
	Keys   []string `json:"keys,omitempty"`
	Note   string   `json:"note,omitempty"`
	// Default es lo que escribía de fábrica, para poder volver a eso.
	Default string `json:"default,omitempty"`
	Builtin bool   `json:"builtin,omitempty"`
	// Changed dice que hay un reemplazo tuyo con esta frase, sea porque la
	// agregaste, porque pisaste un comando de fábrica o porque lo apagaste.
	Changed bool `json:"changed,omitempty"`
	Off     bool `json:"off,omitempty"`
	// Fixed son los que hacen algo que no es escribir texto —una tecla, un
	// borrado—, y por eso no hay valor de config que los reproduzca.
	Fixed bool `json:"fixed,omitempty"`
}

// Entries son todos los comandos como los ve el editor: los de fábrica con lo
// que escriben, pisados por los tuyos donde los haya, y los tuyos que no
// corresponden a ninguno de fábrica.
func Entries(opts Options) []Entry {
	out := make([]Entry, 0, len(catalog)+len(opts.Replacements))
	at := make(map[string]int, len(catalog))
	for phrase, r := range catalog {
		key := normalizePhrase(phrase)
		at[key] = len(out)
		out = append(out, Entry{
			Key:     key,
			Say:     phrase,
			Writes:  r.emit,
			Keys:    r.keys,
			Note:    describe(phrase, r, false).Note,
			Default: r.emit,
			Builtin: true,
			Fixed:   r.op != opNone || len(r.keys) > 0,
		})
	}
	for phrase, emit := range opts.Replacements {
		key := normalizePhrase(phrase)
		if key == "" {
			continue
		}
		// La frase que se muestra es la tuya y no la del catálogo: es la que
		// está en tu archivo, y las dos matchean igual.
		e := Entry{Key: key, Say: phrase, Writes: emit, Changed: true, Off: emit == ""}
		i, builtin := at[key]
		if builtin {
			e.Builtin = true
			e.Default = out[i].Default
			e.Note = out[i].Note
			// Apagado se sigue viendo lo que hacía; pisado con texto, no: un
			// reemplazo tuyo escribe eso y nada más, sin las teclas de antes.
			if e.Off {
				e.Keys, e.Fixed = out[i].Keys, out[i].Fixed
			}
			out[i] = e
			continue
		}
		at[key] = len(out)
		out = append(out, e)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Key != out[j].Key {
			return out[i].Key < out[j].Key
		}
		return out[i].Say < out[j].Say
	})
	return out
}

// NormalizePhrase deja una frase como se la busca en el diccionario, que es lo
// que hace falta afuera para saber si dos frases son el mismo comando.
func NormalizePhrase(phrase string) string { return normalizePhrase(phrase) }

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
