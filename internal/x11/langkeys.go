package x11

import (
	"fmt"
	"sort"
	"time"

	"github.com/jezek/xgb/xproto"
)

// Las teclas de idioma son las que eligen a qué se traduce lo que dictaste: con
// la "e" apretada cuando soltás el combo, el texto se pega en inglés.
//
// Se resuelven contra el mapa de teclado igual que el hotkey, y se miran de dos
// maneras a la vez porque ninguna sola alcanza: los eventos raw dicen cuál se
// apretó última —que es la que gana si hay dos— y XQueryKeymap dice cuál sigue
// hundida de verdad, que es lo único que sobrevive a un release que se perdió
// mientras otra app tenía el teclado agarrado.

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

// languageGrace es cuánto sigue valiendo una tecla de idioma después de
// soltarla.
//
// Soltar el combo y la letra es un solo movimiento de la mano, y los dedos no
// se levantan sincronizados: sin esta gracia, largar la "e" veinte milisegundos
// antes que la tecla del dictado pegaba el texto en castellano y parecía que la
// traducción no funcionaba.
const languageGrace = 300 * time.Millisecond

// langTracker sigue qué teclas de idioma están en juego durante un dictado.
type langTracker struct {
	keys     *LanguageKeys
	seq      int
	down     map[int]int       // keycode → orden en que se apretó
	released map[int]time.Time // keycode → cuándo se soltó
}

func newLangTracker() *langTracker {
	return &langTracker{down: map[int]int{}, released: map[int]time.Time{}}
}

// reset borra lo visto: arranca un dictado nuevo.
func (t *langTracker) reset() {
	t.seq = 0
	clear(t.down)
	clear(t.released)
}

func (t *langTracker) press(code int) {
	t.seq++
	t.down[code] = t.seq
	delete(t.released, code)
}

func (t *langTracker) release(code int) {
	if _, ok := t.down[code]; !ok {
		return
	}
	delete(t.down, code)
	t.released[code] = time.Now()
}

// current es la tecla de idioma que manda ahora mismo, con lo que vimos por
// eventos y lo que confirme el servidor X.
//
// Gana la última apretada: si probaste con la "e" y terminaste en la "p", vale
// la "p".
func (t *langTracker) current(confirmed map[int]bool) (LanguageKey, bool) {
	best, bestSeq := LanguageKey{}, -1
	consider := func(code, seq int) {
		key, ok := t.keys.Lookup(code)
		if !ok || seq <= bestSeq {
			return
		}
		best, bestSeq = key, seq
	}
	for code, seq := range t.down {
		consider(code, seq)
	}
	// Una tecla que X reporta hundida y nosotros no vimos apretar —el press se
	// perdió, o venía de antes del dictado— vale igual, y como no sabemos su
	// orden va última: es la que el usuario tiene el dedo encima ahora.
	for code := range confirmed {
		if _, seen := t.down[code]; seen {
			continue
		}
		consider(code, t.seq+1)
	}
	if bestSeq >= 0 {
		return best, true
	}
	// Nada apretado: la que se acaba de soltar todavía cuenta.
	var newest time.Time
	for code, when := range t.released {
		if time.Since(when) > languageGrace || !when.After(newest) {
			continue
		}
		if key, ok := t.keys.Lookup(code); ok {
			best, newest = key, when
		}
	}
	return best, best.Language != ""
}

// live es lo mismo pero sin preguntarle a X ni esperar la gracia: es lo que se
// dibuja en la ventanita mientras hablás, y ahí lo que importa es que siga al
// dedo en el momento.
func (t *langTracker) live() LanguageKey {
	best, bestSeq := LanguageKey{}, -1
	for code, seq := range t.down {
		if key, ok := t.keys.Lookup(code); ok && seq > bestSeq {
			best, bestSeq = key, seq
		}
	}
	return best
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
