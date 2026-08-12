package x11

import (
	"fmt"
	"strings"
	"time"

	"github.com/jezek/xgb/xproto"
	"github.com/jezek/xgb/xtest"
)

// TerminalClasses son las ventanas donde Ctrl+V no pega: las terminales usan
// Ctrl+Shift+V.
var TerminalClasses = map[string]bool{
	"konsole": true, "yakuake": true, "xterm": true, "uxterm": true,
	"gnome-terminal": true, "gnome-terminal-server": true, "alacritty": true,
	"kitty": true, "wezterm": true, "terminator": true, "tilix": true,
	"xfce4-terminal": true, "urxvt": true, "rxvt": true, "st": true,
	"foot": true, "ghostty": true, "hyper": true, "warp": true,
}

// IsTerminal dice si en esa clase de ventana hay que pegar con Ctrl+Shift+V.
func IsTerminal(class string) bool { return TerminalClasses[strings.ToLower(class)] }

// Target es la ventana a la que le estamos dictando.
type Target struct {
	Window xproto.Window `json:"window"`
	Class  string        `json:"class"`
}

// ActiveWindow es la ventana enfocada ahora mismo, con su clase.
//
// Se la guarda ANTES de grabar: cuando el dictado termina el foco puede haberse
// ido a cualquier parte, y con el id lo traemos de vuelta.
func (c *Conn) ActiveWindow() Target {
	active, err := c.Atom("_NET_ACTIVE_WINDOW")
	if err != nil {
		return Target{}
	}
	reply, err := xproto.GetProperty(c.X, false, c.Root, active,
		xproto.AtomWindow, 0, 1).Reply()
	if err != nil || len(reply.Value) < 4 {
		return Target{}
	}
	win := xproto.Window(uint32(reply.Value[0]) | uint32(reply.Value[1])<<8 |
		uint32(reply.Value[2])<<16 | uint32(reply.Value[3])<<24)
	if win == 0 {
		return Target{}
	}
	return Target{Window: win, Class: c.WindowClass(win)}
}

// WindowClass es el segundo campo de WM_CLASS, en minúsculas.
func (c *Conn) WindowClass(win xproto.Window) string {
	if win == 0 {
		return ""
	}
	reply, err := xproto.GetProperty(c.X, false, win, xproto.AtomWmClass,
		xproto.AtomString, 0, 256).Reply()
	if err != nil || len(reply.Value) == 0 {
		return ""
	}
	// WM_CLASS son dos strings terminados en NUL: instancia y clase.
	parts := strings.Split(strings.TrimRight(string(reply.Value), "\x00"), "\x00")
	return strings.ToLower(parts[len(parts)-1])
}

// Focus trae la ventana al frente por EWMH, y si el WM no coopera le pone el
// foco de entrada a mano.
func (c *Conn) Focus(win xproto.Window) bool {
	if win == 0 {
		return false
	}
	active, err := c.Atom("_NET_ACTIVE_WINDOW")
	if err == nil {
		data := make([]uint32, 5)
		data[0] = 2 // el pedido viene de una herramienta, no del usuario
		data[1] = uint32(time.Now().UnixMilli() & 0xffffffff)
		event := xproto.ClientMessageEvent{
			Format: 32,
			Window: win,
			Type:   active,
			Data:   xproto.ClientMessageDataUnionData32New(data),
		}
		_ = xproto.SendEventChecked(c.X, false, c.Root,
			xproto.EventMaskSubstructureNotify|xproto.EventMaskSubstructureRedirect,
			string(event.Bytes())).Check()
	}
	err = xproto.SetInputFocusChecked(c.X, xproto.InputFocusParent, win,
		xproto.TimeCurrentTime).Check()
	return err == nil
}

// EnableXTest confirma que el servidor puede sintetizar teclas.
func (c *Conn) EnableXTest() error {
	if err := xtest.Init(c.X); err != nil {
		return fmt.Errorf("el servidor X no tiene la extensión XTEST: %w", err)
	}
	return nil
}

const (
	keyPress   = 2
	keyRelease = 3
)

func (c *Conn) fakeKey(kind byte, code int) error {
	return xtest.FakeInputChecked(c.X, kind, byte(code), 0, c.Root, 0, 0, 0).Check()
}

// modifiersDown son los keycodes de modificadores que están apretados ahora.
//
// Es el `--clearmodifiers` de xdotool: si soltás el dictado con AltGr todavía
// hundida, un Ctrl+V sintético saldría como Ctrl+AltGr+V y no pegaría nada.
func (c *Conn) modifiersDown() ([]int, error) {
	mapping, err := xproto.GetModifierMapping(c.X).Reply()
	if err != nil {
		return nil, err
	}
	down, err := c.KeysDown()
	if err != nil {
		return nil, err
	}
	var held []int
	for _, code := range mapping.Keycodes {
		if code != 0 && down[int(code)] {
			held = append(held, int(code))
		}
	}
	return held, nil
}

// SendCombo teclea una combinación tipo "ctrl+shift+v" al foco actual.
//
// Va sin ventana destino a propósito: mandarle el evento a una ventana puntual
// significa XSendEvent, y media docena de toolkits descarta los eventos
// sintéticos. Activamos la ventana y tecleamos al foco real.
func (c *Conn) SendCombo(spec string) error {
	keymap, err := c.LoadKeymap()
	if err != nil {
		return err
	}
	var codes []int
	for _, name := range strings.Split(spec, "+") {
		resolved, err := resolve(keymap, strings.TrimSpace(name))
		if err != nil {
			return err
		}
		codes = append(codes, first(resolved))
	}

	held, _ := c.modifiersDown()
	for _, code := range held {
		_ = c.fakeKey(keyRelease, code)
	}
	defer func() {
		for _, code := range held {
			_ = c.fakeKey(keyPress, code)
		}
	}()

	for _, code := range codes {
		if err := c.fakeKey(keyPress, code); err != nil {
			return err
		}
	}
	for i := len(codes) - 1; i >= 0; i-- {
		if err := c.fakeKey(keyRelease, codes[i]); err != nil {
			return err
		}
	}
	return nil
}

func first(set map[int]bool) int {
	best := -1
	for code := range set {
		if best < 0 || code < best {
			best = code
		}
	}
	return best
}

// TypeText escribe el texto tecla por tecla.
//
// Para los caracteres que el teclado actual no puede producir se toma prestado
// un keycode libre, se le carga el keysym que hace falta y se lo devuelve como
// estaba. Es lo mismo que hace `xdotool type`, y es la única forma de tipear
// algo que no está en tu layout.
func (c *Conn) TypeText(text string, delay time.Duration) error {
	keymap, err := c.LoadKeymap()
	if err != nil {
		return err
	}
	spare := keymap.spareKeycode()

	held, _ := c.modifiersDown()
	for _, code := range held {
		_ = c.fakeKey(keyRelease, code)
	}
	defer func() {
		for _, code := range held {
			_ = c.fakeKey(keyPress, code)
		}
	}()

	shift := 0
	if codes, err := keymap.KeycodesFor("Shift_L"); err == nil && len(codes) > 0 {
		shift = codes[0]
	}

	for _, r := range text {
		code, needShift, ok := keymap.lookup(r)
		if !ok {
			if spare == 0 {
				return fmt.Errorf("no puedo tipear %q: no hay keycodes libres en el teclado", r)
			}
			if err := c.remap(spare, runeKeysym(r)); err != nil {
				return err
			}
			code, needShift = spare, false
			// El servidor tiene que digerir el mapa nuevo antes del evento.
			time.Sleep(12 * time.Millisecond)
		}
		if needShift && shift != 0 {
			_ = c.fakeKey(keyPress, shift)
		}
		_ = c.fakeKey(keyPress, code)
		_ = c.fakeKey(keyRelease, code)
		if needShift && shift != 0 {
			_ = c.fakeKey(keyRelease, shift)
		}
		if delay > 0 {
			time.Sleep(delay)
		}
	}
	if spare != 0 {
		_ = c.remap(spare, 0) // devolver el keycode prestado
	}
	return nil
}

// remap le carga un keysym a un keycode libre.
func (c *Conn) remap(code int, sym uint32) error {
	syms := []xproto.Keysym{xproto.Keysym(sym), xproto.Keysym(sym)}
	return xproto.ChangeKeyboardMappingChecked(c.X, 1, xproto.Keycode(code), 2, syms).Check()
}

// runeKeysym es el keysym de un carácter: Latin-1 va directo y el resto usa la
// convención Unicode de X (0x01000000 | codepoint).
func runeKeysym(r rune) uint32 {
	if r >= 0x20 && r <= 0xff {
		return uint32(r)
	}
	return 0x01000000 | uint32(r)
}

// lookup busca qué tecla (y si con Shift) produce ese carácter.
func (k *Keymap) lookup(r rune) (code int, shift, ok bool) {
	sym := runeKeysym(r)
	for i := 0; i+k.PerKeycode <= len(k.Keysyms); i += k.PerKeycode {
		group := k.Keysyms[i : i+k.PerKeycode]
		for level, s := range group {
			if s != sym {
				continue
			}
			if level > 1 { // niveles 3 y 4 piden AltGr: no vale la pena
				continue
			}
			return k.MinKeycode + i/k.PerKeycode, level == 1, true
		}
	}
	return 0, false, false
}

// spareKeycode busca un keycode sin nada asignado para pedirlo prestado.
func (k *Keymap) spareKeycode() int {
	for i := 0; i+k.PerKeycode <= len(k.Keysyms); i += k.PerKeycode {
		empty := true
		for _, s := range k.Keysyms[i : i+k.PerKeycode] {
			if s != 0 {
				empty = false
				break
			}
		}
		if empty {
			return k.MinKeycode + i/k.PerKeycode
		}
	}
	return 0
}
