// Package commands convierte lo que dictaste en lo que hay que escribir.
//
// Decir "abre pregunta cómo andás signo de pregunta" tiene que terminar en
// "¿cómo andás?", y decir "entre corchetes" tiene que dejar el cursor entre los
// dos corchetes. Lo primero es un reemplazo de texto; lo segundo no existe como
// texto: es una tecla.
//
// Por eso el resultado no es un string sino un [Plan]: una lista de tramos que
// son texto para escribir o teclas para mandar. El que dicta sin usar ningún
// comando obtiene un plan de un solo tramo, que es exactamente lo de antes.
package commands

import (
	"strings"
	"unicode"

	"github.com/neitanod/dictador/internal/config"
)

// Step es un tramo del dictado ya resuelto: o texto, o una tecla.
type Step struct {
	Text string
	Key  string // combo tal como lo entiende x11.SendCombo ("Return", "ctrl+BackSpace")
}

// Plan es el dictado entero, listo para ejecutar en orden.
type Plan struct {
	Steps []Step
}

// Empty dice si no quedó nada para hacer.
func (p Plan) Empty() bool { return len(p.Steps) == 0 }

// HasKeys dice si hay teclas de por medio, o sea si el texto no alcanza.
func (p Plan) HasKeys() bool {
	for _, s := range p.Steps {
		if s.Key != "" {
			return true
		}
	}
	return false
}

// Text es el plan aplanado a un string, que es lo que va al clipboard, al
// historial y a la ventanita.
//
// Aplanar es ejecutar el plan contra un editor imaginario: el Left de "entre
// corchetes" mueve un cursor de verdad, y por eso "entre corchetes hola" se lee
// "[hola]" y no "[]hola".
func (p Plan) Text() string {
	var text []rune
	cursor := 0
	insert := func(s string) {
		runes := []rune(s)
		tail := append([]rune(nil), text[cursor:]...)
		text = append(append(text[:cursor], runes...), tail...)
		cursor += len(runes)
	}
	for _, s := range p.Steps {
		switch s.Key {
		case "":
			insert(s.Text)
		case "Return":
			insert("\n")
		case "Tab":
			insert("\t")
		case "Left":
			if cursor > 0 {
				cursor--
			}
		case "ctrl+BackSpace":
			// El borrado que le pedimos al editor: acá sólo puede alcanzar a lo
			// que el propio dictado haya escrito antes.
			start := cursor
			for start > 0 && text[start-1] == ' ' {
				start--
			}
			for start > 0 && text[start-1] != ' ' {
				start--
			}
			text = append(text[:start], text[cursor:]...)
			cursor = start
		}
	}
	return string(text)
}

// StripFinalPeriod saca el punto del final, si el dictado termina en texto.
func (p *Plan) StripFinalPeriod() {
	if len(p.Steps) == 0 {
		return
	}
	last := &p.Steps[len(p.Steps)-1]
	if last.Key == "" && strings.HasSuffix(last.Text, ".") {
		last.Text = strings.TrimSuffix(last.Text, ".")
		if last.Text == "" {
			p.Steps = p.Steps[:len(p.Steps)-1]
		}
	}
}

// AppendText agrega texto al final del plan.
func (p *Plan) AppendText(text string) {
	if text == "" {
		return
	}
	if n := len(p.Steps); n > 0 && p.Steps[n-1].Key == "" {
		p.Steps[n-1].Text += text
		return
	}
	p.Steps = append(p.Steps, Step{Text: text})
}

// Options es cómo se compila: si los comandos están prendidos y qué agregó el
// usuario en su config.
type Options struct {
	Enabled      bool
	Replacements map[string]string
}

// OptionsFrom saca las opciones de la configuración cargada.
func OptionsFrom(cfg config.Config) Options {
	return Options{
		Enabled:      cfg.Commands.Enabled,
		Replacements: cfg.Commands.Replacements,
	}
}

// Compile arma el plan a partir del texto que devolvió el motor de voz.
//
// Con los comandos apagados sigue haciendo una cosa: normalizar los espacios,
// que es lo que el postproceso hacía desde siempre.
func Compile(text string, opts Options) Plan {
	words := strings.Fields(text)
	var b builder
	if !opts.Enabled {
		for _, w := range words {
			b.word(w)
		}
		return b.plan()
	}

	rules, longest := ruleSet(opts.Replacements)
	norms := make([]string, len(words))
	for i, w := range words {
		norms[i] = normalize(w)
	}

	for i := 0; i < len(words); {
		// Gana la frase más larga: si no, "punto y coma" se comería en "punto"
		// y "guión bajo" en "guión".
		matched := false
		for n := longest; n >= 1 && !matched; n-- {
			if i+n > len(words) {
				continue
			}
			key := strings.Join(norms[i:i+n], " ")
			if strings.TrimSpace(key) == "" {
				continue
			}
			rule, ok := rules[key]
			if !ok {
				continue
			}
			b.apply(rule)
			i += n
			matched = true
		}
		if !matched {
			b.word(words[i])
			i++
		}
	}
	return b.plan()
}

// builder va armando los tramos y se acuerda de si hace falta un espacio.
type builder struct {
	steps []Step
	// fresh dice que el tramo actual arranca de cero: no hay que separar nada.
	fresh bool
	// tight dice que lo último escrito se pega a lo que venga ("¿", "/").
	tight bool
}

func (b *builder) plan() Plan { return Plan{Steps: b.steps} }

// lastRune es el último carácter escrito, o 0 si todavía no hay ninguno.
func (b *builder) lastRune() rune {
	for i := len(b.steps) - 1; i >= 0; i-- {
		if b.steps[i].Key != "" {
			return 0 // hubo una tecla de por medio: el renglón arrancó de nuevo
		}
		if runes := []rune(b.steps[i].Text); len(runes) > 0 {
			return runes[len(runes)-1]
		}
	}
	return 0
}

func (b *builder) writeText(text string) {
	if n := len(b.steps); n > 0 && b.steps[n-1].Key == "" {
		b.steps[n-1].Text += text
		return
	}
	b.steps = append(b.steps, Step{Text: text})
}

func (b *builder) addKey(key string) {
	b.steps = append(b.steps, Step{Key: key})
	b.fresh, b.tight = true, false
}

// word escribe una palabra normal, con su espacio adelante si corresponde.
func (b *builder) word(w string) {
	if len(b.steps) > 0 && !b.fresh && !b.tight {
		b.writeText(" ")
	}
	b.writeText(w)
	b.fresh, b.tight = false, false
}

func (b *builder) apply(r rule) {
	switch r.op {
	case opDeleteWord:
		b.deleteWord()
		return
	case opDeleteAll:
		b.deleteAll()
		return
	}
	emit := r.emit
	// "signo de pregunta punto y aparte" no puede escribir "?.": el punto ya
	// está puesto por el signo que cierra, y lo único que falta es la línea
	// nueva.
	if emit == "." && strings.ContainsRune(".?!…", b.lastRune()) {
		emit = ""
	}
	if emit != "" {
		if len(b.steps) > 0 && !b.fresh && !b.tight && !r.tightLeft {
			b.writeText(" ")
		}
		b.writeText(emit)
		b.fresh, b.tight = false, r.tightRight
	}
	for _, key := range r.keys {
		b.addKey(key)
	}
}

// deleteWord saca la última palabra dictada. Si todavía no dictaste ninguna, le
// pide al editor que borre la que él tenga atrás.
func (b *builder) deleteWord() {
	n := len(b.steps)
	if n == 0 || b.steps[n-1].Key != "" || b.steps[n-1].Text == "" {
		b.addKey("ctrl+BackSpace")
		return
	}
	trimmed := strings.TrimRight(b.steps[n-1].Text, " ")
	if idx := strings.LastIndex(trimmed, " "); idx >= 0 {
		b.steps[n-1].Text = trimmed[:idx]
	} else {
		b.steps = b.steps[:n-1]
	}
	b.fresh = len(b.steps) == 0 || b.steps[len(b.steps)-1].Key != ""
	b.tight = false
}

// deleteAll tira todo lo dictado hasta acá.
//
// Sólo lo dictado: si lo decís antes de dictar nada, no hace nada. El texto que
// ya estaba en tu editor no lo escribimos nosotros y no es nuestro para
// borrarlo.
func (b *builder) deleteAll() {
	if len(b.steps) == 0 {
		return
	}
	b.steps = nil
	b.fresh, b.tight = true, false
}

// tildes es lo que hay que sacar para que "guión" y "guion" sean la misma orden.
var tildes = strings.NewReplacer(
	"á", "a", "é", "e", "í", "i", "ó", "o", "ú", "u", "ü", "u",
	"à", "a", "è", "e", "ì", "i", "ò", "o", "ù", "u",
	"â", "a", "ê", "e", "î", "i", "ô", "o", "û", "u",
)

// normalize deja una palabra como se la busca en el diccionario: en minúsculas,
// sin tildes y sin la puntuación que el motor de voz le haya colgado.
//
// Esa puntuación importa más de lo que parece: whisper escribe "Punto y
// aparte." con el punto pegado, y sin sacarlo el comando no matchea nunca.
func normalize(word string) string {
	word = tildes.Replace(strings.ToLower(word))
	return strings.TrimFunc(word, func(r rune) bool {
		return unicode.IsPunct(r) || unicode.IsSymbol(r) || unicode.IsSpace(r)
	})
}
