package x11

import (
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"

	"github.com/jezek/xgb/xproto"
)

// Aliases son los nombres cómodos para las teclas que a nadie le gusta buscar
// en xmodmap. Los genéricos aceptan cualquiera de los dos lados del teclado.
var Aliases = map[string][]string{
	"altgr":     {"ISO_Level3_Shift"},
	"ctrl":      {"Control_L", "Control_R"},
	"control":   {"Control_L", "Control_R"},
	"alt":       {"Alt_L", "Alt_R"},
	"shift":     {"Shift_L", "Shift_R"},
	"super":     {"Super_L", "Super_R"},
	"meta":      {"Super_L", "Super_R"},
	"win":       {"Super_L", "Super_R"},
	"rctrl":     {"Control_R"},
	"rightctrl": {"Control_R"},
	"lctrl":     {"Control_L"},
	"leftctrl":  {"Control_L"},
	"ralt":      {"Alt_R"},
	"rightalt":  {"Alt_R"},
	"menu":      {"Menu"},
	"capslock":  {"Caps_Lock"},
}

var (
	nameOnce  sync.Once
	nameBySym map[uint32]string
)

// KeysymName es el nombre simbólico de un keysym ("Control_R", "a"), o vacío.
//
// La tabla generada va de nombre a número; el diccionario inverso se arma una
// vez y se queda con el primer nombre de cada valor, que es el canónico.
func KeysymName(sym uint32) string {
	nameOnce.Do(func() {
		nameBySym = make(map[uint32]string, len(keysymByName))
		names := make([]string, 0, len(keysymByName))
		for name := range keysymByName {
			names = append(names, name)
		}
		sort.Strings(names)
		for _, name := range names {
			sym := keysymByName[name]
			if _, ok := nameBySym[sym]; !ok {
				nameBySym[sym] = name
			}
		}
	})
	if name, ok := nameBySym[sym]; ok {
		return name
	}
	// Los keysym Unicode son 0x01000000 | codepoint.
	if sym&0xff000000 == 0x01000000 {
		return string(rune(sym & 0x00ffffff))
	}
	return ""
}

// Keysym busca el número de un nombre simbólico.
func Keysym(name string) (uint32, bool) {
	sym, ok := keysymByName[name]
	return sym, ok
}

// Key es una tecla del mapa actual: su keycode y cómo se llama.
type Key struct {
	Keycode int    `json:"keycode"`
	Name    string `json:"name"`
}

// Keymap es el mapa de teclado que reportó el servidor.
type Keymap struct {
	MinKeycode int
	PerKeycode int
	Keysyms    []uint32
}

// LoadKeymap le pregunta a X qué produce cada tecla.
func (c *Conn) LoadKeymap() (*Keymap, error) {
	min := int(c.Setup.MinKeycode)
	count := int(c.Setup.MaxKeycode) - min + 1
	reply, err := xproto.GetKeyboardMapping(c.X, xproto.Keycode(min), byte(count)).Reply()
	if err != nil {
		return nil, fmt.Errorf("no pude leer el mapa de teclado: %w", err)
	}
	syms := make([]uint32, len(reply.Keysyms))
	for i, s := range reply.Keysyms {
		syms[i] = uint32(s)
	}
	return &Keymap{
		MinKeycode: min,
		PerKeycode: int(reply.KeysymsPerKeycode),
		Keysyms:    syms,
	}, nil
}

// KeycodesFor devuelve todos los keycodes que producen ese keysym: puede haber
// más de uno, y hay teclados donde los hay.
func (k *Keymap) KeycodesFor(name string) ([]int, error) {
	sym, ok := keysymByName[name]
	if !ok {
		return nil, fmt.Errorf("keysym desconocido: %q", name)
	}
	var codes []int
	for i := 0; i+k.PerKeycode <= len(k.Keysyms); i += k.PerKeycode {
		for _, s := range k.Keysyms[i : i+k.PerKeycode] {
			if s == sym {
				codes = append(codes, k.MinKeycode+i/k.PerKeycode)
				break
			}
		}
	}
	if len(codes) == 0 {
		return nil, fmt.Errorf("la tecla %q no está en el mapa de teclado actual", name)
	}
	return codes, nil
}

// Keys lista (keycode, nombre) del mapa actual, para elegir el hotkey.
func (k *Keymap) Keys() []Key {
	var out []Key
	for i := 0; i+k.PerKeycode <= len(k.Keysyms); i += k.PerKeycode {
		for _, sym := range k.Keysyms[i : i+k.PerKeycode] {
			if sym == 0 {
				continue
			}
			name := KeysymName(sym)
			if name == "" {
				name = fmt.Sprintf("0x%x", sym)
			}
			out = append(out, Key{Keycode: k.MinKeycode + i/k.PerKeycode, Name: name})
			break
		}
	}
	return out
}

// Combo es una tecla gatillo, opcionalmente con modificadores que la acompañan.
//
// Se escribe "AltGr+Control_R": lo último es el gatillo y lo de antes son
// teclas que tienen que estar apretadas. Un nombre solo también vale.
type Combo struct {
	Spec        string
	TriggerName string
	ModNames    []string
	Trigger     map[int]bool
	Mods        []map[int]bool
	All         map[int]bool
}

// ParseCombo resuelve la especificación contra el mapa de teclado de la máquina.
func ParseCombo(spec string, k *Keymap) (*Combo, error) {
	var parts []string
	for _, p := range strings.Split(spec, "+") {
		if p = strings.TrimSpace(p); p != "" {
			parts = append(parts, p)
		}
	}
	if len(parts) == 0 {
		return nil, errors.New("la tecla del dictado está vacía")
	}
	c := &Combo{
		Spec:        spec,
		TriggerName: parts[len(parts)-1],
		ModNames:    parts[:len(parts)-1],
		All:         map[int]bool{},
	}
	var err error
	if c.Trigger, err = resolve(k, c.TriggerName); err != nil {
		return nil, err
	}
	for code := range c.Trigger {
		c.All[code] = true
	}
	for _, name := range c.ModNames {
		mod, err := resolve(k, name)
		if err != nil {
			return nil, err
		}
		c.Mods = append(c.Mods, mod)
		for code := range mod {
			c.All[code] = true
		}
	}
	return c, nil
}

func resolve(k *Keymap, name string) (map[int]bool, error) {
	names, ok := Aliases[strings.ToLower(name)]
	if !ok {
		names = []string{name}
	}
	codes := map[int]bool{}
	var problems []string
	for _, keysymName := range names {
		found, err := k.KeycodesFor(keysymName)
		if err != nil {
			problems = append(problems, err.Error())
			continue
		}
		for _, code := range found {
			codes[code] = true
		}
	}
	if len(codes) == 0 {
		if len(problems) > 0 {
			return nil, errors.New(strings.Join(problems, "; "))
		}
		return nil, fmt.Errorf("tecla desconocida: %q", name)
	}
	return codes, nil
}

// Describe explica el combo como lo mostraría el doctor.
func (c *Combo) Describe() string {
	codes := make([]int, 0, len(c.Trigger))
	for code := range c.Trigger {
		codes = append(codes, code)
	}
	sort.Ints(codes)
	list := make([]string, len(codes))
	for i, code := range codes {
		list[i] = fmt.Sprint(code)
	}
	if len(c.ModNames) == 0 {
		return fmt.Sprintf("%s (keycode %s)", c.TriggerName, strings.Join(list, ", "))
	}
	return fmt.Sprintf("%s + %s (keycode %s)",
		strings.Join(c.ModNames, "+"), c.TriggerName, strings.Join(list, ", "))
}

// KeysDown pregunta qué teclas están físicamente apretadas.
//
// Preguntárselo a X en vez de acumular eventos nos salva de los releases que se
// pierden cuando otra app tiene el teclado agarrado: si no, un modificador
// quedaría marcado como apretado para siempre.
func (c *Conn) KeysDown() (map[int]bool, error) {
	reply, err := xproto.QueryKeymap(c.X).Reply()
	if err != nil {
		return nil, err
	}
	down := map[int]bool{}
	for kc := 0; kc < 256 && kc/8 < len(reply.Keys); kc++ {
		if reply.Keys[kc/8]&(1<<(kc%8)) != 0 {
			down[kc] = true
		}
	}
	return down, nil
}

// held dice si todos los modificadores del combo están apretados.
func (c *Combo) held(down map[int]bool) bool {
	for _, mod := range c.Mods {
		ok := false
		for code := range mod {
			if down[code] {
				ok = true
				break
			}
		}
		if !ok {
			return false
		}
	}
	return true
}
