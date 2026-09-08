package x11

import (
	"fmt"
	"sort"

	"github.com/jezek/xgb/xproto"
)

// Las teclas de idioma son las que eligen a qué se traduce lo que dictaste:
// tocás la "e" mientras hablás y el texto se pega en inglés.
//
// Se resuelven contra el mapa de teclado igual que el hotkey, y funcionan como
// interruptores: un toque prende, otro apaga, y otra letra pisa a la anterior.

// LanguageKeys es la tabla letra → idioma ya resuelta a keycodes.
type LanguageKeys struct {
	codes map[int]LanguageKey
}

// LanguageKey es una tecla de idioma: la letra tal como se escribe en el config
// y el idioma al que manda.
type LanguageKey struct {
	Key      string
	Language string
}

// NewLanguageKeys resuelve la tabla contra el teclado de esta máquina.
//
// Los errores son por letra y no cortan: una letra que este teclado no tiene
// deja de funcionar y las demás siguen andando, que es mejor que quedarse sin
// traducción entera por una fila mal escrita.
func NewLanguageKeys(table map[string]string, k *Keymap) (*LanguageKeys, []error) {
	lk := &LanguageKeys{codes: map[int]LanguageKey{}}
	var problems []error
	names := make([]string, 0, len(table))
	for name := range table {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		language := table[name]
		if name == "" || language == "" {
			continue
		}
		codes, err := resolve(k, name)
		if err != nil {
			problems = append(problems, fmt.Errorf("la tecla %q de la traducción: %w", name, err))
			continue
		}
		for code := range codes {
			lk.codes[code] = LanguageKey{Key: name, Language: language}
		}
	}
	return lk, problems
}

// Empty dice si no quedó ninguna tecla de idioma en juego.
func (lk *LanguageKeys) Empty() bool { return lk == nil || len(lk.codes) == 0 }

// Keycodes son las teclas a agarrar mientras dura el dictado.
func (lk *LanguageKeys) Keycodes() []int {
	if lk == nil {
		return nil
	}
	out := make([]int, 0, len(lk.codes))
	for code := range lk.codes {
		out = append(out, code)
	}
	sort.Ints(out)
	return out
}

// Lookup dice si ese keycode es una tecla de idioma.
func (lk *LanguageKeys) Lookup(code int) (LanguageKey, bool) {
	if lk == nil {
		return LanguageKey{}, false
	}
	key, ok := lk.codes[code]
	return key, ok
}

// langTracker sigue qué idioma quedó elegido durante un dictado.
//
// La letra es un interruptor y no un botón que se mantiene: se aprieta una vez
// y el dictado sale traducido, se aprieta de nuevo y sale como se dijo. Otra
// letra pisa a la anterior. Empezó al revés —había que tenerla hundida al
// soltar el combo— y era incómodo de verdad: mantener dos teclas mientras
// hablás ocupa la mano entera.
type langTracker struct {
	keys *LanguageKeys
	// elegido es el idioma prendido ahora mismo, o vacío si no hay ninguno.
	elegido LanguageKey
}

func newLangTracker() *langTracker { return &langTracker{} }

// reset arranca un dictado nuevo con el idioma que le digan.
//
// Con el idioma pegajoso apagado eso es "ninguno", y hay que tocar la letra en
// cada dictado: un dictado que sale traducido sin que nadie lo pidiera se
// descubre después de pegarlo. Con el pegajoso prendido arranca con el último,
// que es lo que sirve para una conversación entera en otro idioma.
func (t *langTracker) reset(inicial LanguageKey) {
	t.elegido = inicial
}

// press es un toque de una tecla de idioma: prende, apaga o cambia.
//
// Los que vienen marcados como repetidos se ignoran. X los manda solo mientras
// mantenés la tecla hundida, y contarlos prendería y apagaría el idioma treinta
// veces por segundo. Que se sepan repetidos por la marca del evento y no por
// llevar la cuenta de qué está apretado es lo único que funciona acá: mientras
// dura el dictado tenemos la tecla agarrada, y con el agarre puesto el release
// no vuelve nunca.
func (t *langTracker) press(code int, repetido bool) {
	if repetido {
		return
	}
	key, ok := t.keys.Lookup(code)
	if !ok {
		return
	}
	if t.elegido.Key == key.Key {
		t.elegido = LanguageKey{} // el mismo interruptor, apagado
		return
	}
	t.elegido = key
}

// current es el idioma con el que se va a traducir este dictado.
func (t *langTracker) current() (LanguageKey, bool) {
	return t.elegido, t.elegido.Language != ""
}

// GrabKeys se queda con esas teclas: mientras dure el grab, apretarlas no llega
// a la aplicación de enfrente.
//
// Es lo que evita que elegir el idioma escriba la letra en el medio del texto,
// o peor, dispare un atajo: Ctrl+E con el combo apretado abre la barra de
// búsqueda del navegador. Se toma cuando empieza a grabar y se suelta al
// terminar, así fuera del dictado la tecla es una tecla como cualquier otra.
func (c *Conn) GrabKeys(codes []int) error {
	var last error
	for _, code := range codes {
		cookie := xproto.GrabKeyChecked(c.X, false, c.Root, xproto.ModMaskAny,
			xproto.Keycode(code), xproto.GrabModeAsync, xproto.GrabModeAsync)
		if err := cookie.Check(); err != nil {
			last = fmt.Errorf("no pude agarrar el keycode %d: %w", code, err)
		}
	}
	return last
}

// UngrabKeys devuelve las teclas al resto del sistema.
func (c *Conn) UngrabKeys(codes []int) {
	for _, code := range codes {
		_ = xproto.UngrabKeyChecked(c.X, xproto.Keycode(code), c.Root, xproto.ModMaskAny).Check()
	}
}
