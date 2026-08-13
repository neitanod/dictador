// Comando receiver: una ventana que recibe el pegado, para el test end-to-end.
//
// Es el equivalente del campo de texto al que le dictás: se abre, toma el foco
// y espera un Ctrl+V (o Ctrl+Shift+V, como manda en las terminales). Cuando lo
// recibe pide el contenido del portapapeles y lo escribe en un archivo, que es
// lo que el test lee para saber si el texto hizo el camino completo.
//
//	receiver <archivo de salida> [clase de ventana]
//
// Con --editor hace de campo de texto de verdad: mantiene un buffer y un
// cursor, y va aplicando lo que le llega —el texto tipeado, los pegados, el
// Left que retrocede, el Ctrl+Backspace que borra—. Es la única forma de probar
// que "entre corchetes" deja el cursor adentro: eso no se ve en el portapapeles,
// se ve en lo que termina escrito.
package main

import (
	"fmt"
	"os"
	"time"

	"github.com/jezek/xgb"
	"github.com/jezek/xgb/xproto"
)

func main() {
	args := os.Args[1:]
	editor := false
	if len(args) > 0 && args[0] == "--editor" {
		editor, args = true, args[1:]
	}
	if len(args) < 1 {
		fmt.Fprintln(os.Stderr, "uso: receiver [--editor] <archivo> [clase]")
		os.Exit(2)
	}
	outPath := args[0]
	class := "dictador-receiver"
	if len(args) > 1 {
		class = args[1]
	}

	x, err := xgb.NewConn()
	must(err)
	defer x.Close()

	setup := xproto.Setup(x)
	screen := setup.DefaultScreen(x)

	win, err := xproto.NewWindowId(x)
	must(err)
	must(xproto.CreateWindowChecked(x, screen.RootDepth, win, screen.Root,
		0, 0, 400, 200, 0, xproto.WindowClassInputOutput, screen.RootVisual,
		xproto.CwBackPixel|xproto.CwEventMask,
		[]uint32{screen.WhitePixel,
			xproto.EventMaskKeyPress | xproto.EventMaskPropertyChange |
				xproto.EventMaskStructureNotify}).Check())

	// WM_CLASS es lo que la app mira para decidir si pega con Ctrl+V o con
	// Ctrl+Shift+V, así que el test necesita poder elegirla.
	wmClass := class + "\x00" + class + "\x00"
	must(xproto.ChangePropertyChecked(x, xproto.PropModeReplace, win,
		xproto.AtomWmClass, xproto.AtomString, 8,
		uint32(len(wmClass)), []byte(wmClass)).Check())
	must(xproto.ChangePropertyChecked(x, xproto.PropModeReplace, win,
		xproto.AtomWmName, xproto.AtomString, 8,
		uint32(len("receiver")), []byte("receiver")).Check())

	must(xproto.MapWindowChecked(x, win).Check())
	time.Sleep(300 * time.Millisecond)
	_ = xproto.SetInputFocusChecked(x, xproto.InputFocusParent, win,
		xproto.TimeCurrentTime).Check()

	clipboard := atom(x, "CLIPBOARD")
	utf8 := atom(x, "UTF8_STRING")
	target := atom(x, "DICTADOR_PASTE")

	keymap := loadKeymap(x, setup)
	vKeycodes := keycodesFor(keymap, 'v')

	fmt.Fprintln(os.Stderr, "receiver listo")
	if editor {
		runEditor(x, win, outPath, clipboard, utf8, target, vKeycodes)
		return
	}
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		event, xerr := x.WaitForEvent()
		if xerr != nil || event == nil {
			continue
		}
		switch ev := event.(type) {
		case xproto.KeyPressEvent:
			if !vKeycodes[int(ev.Detail)] {
				continue
			}
			if ev.State&xproto.ModMaskControl == 0 {
				continue // sin Control no es un pegado
			}
			// Pedirle el contenido al dueño de la selección: el daemon.
			must(xproto.ConvertSelectionChecked(x, win, clipboard, utf8, target,
				ev.Time).Check())

		case xproto.SelectionNotifyEvent:
			if ev.Property == 0 {
				fail("el dueño de la selección no me dio nada")
			}
			reply, err := xproto.GetProperty(x, true, win, target,
				xproto.GetPropertyTypeAny, 0, 1<<16).Reply()
			must(err)
			must(os.WriteFile(outPath, reply.Value, 0o644))
			fmt.Fprintf(os.Stderr, "pegado: %s\n", reply.Value)
			return
		}
	}
	fail("nadie me pegó nada en 30s")
}

// Los keysyms que no son un carácter sino un movimiento.
const (
	symBackSpace = 0xff08
	symTab       = 0xff09
	symReturn    = 0xff0d
	symLeft      = 0xff51
)

// runEditor hace de campo de texto: aplica cada tecla y cada pegado sobre un
// buffer con cursor, y cuando pasa un segundo sin que llegue nada escribe lo
// que quedó.
func runEditor(x *xgb.Conn, win xproto.Window, outPath string,
	clipboard, utf8, target xproto.Atom, vKeycodes map[int]bool) {
	var text []rune
	cursor := 0
	insert := func(s string) {
		runes := []rune(s)
		tail := append([]rune(nil), text[cursor:]...)
		text = append(append(text[:cursor], runes...), tail...)
		cursor += len(runes)
	}

	deadline := time.Now().Add(30 * time.Second)
	quiet := time.Now().Add(30 * time.Second) // se acorta con la primera tecla
	for time.Now().Before(deadline) && time.Now().Before(quiet) {
		event, xerr := pollEvent(x)
		if xerr != nil {
			continue
		}
		if event == nil {
			time.Sleep(20 * time.Millisecond)
			continue
		}
		switch ev := event.(type) {
		case xproto.KeyPressEvent:
			quiet = time.Now().Add(1500 * time.Millisecond)
			control := ev.State&xproto.ModMaskControl != 0
			if control && vKeycodes[int(ev.Detail)] {
				must(xproto.ConvertSelectionChecked(x, win, clipboard, utf8,
					target, ev.Time).Check())
				continue
			}
			// El keysym se pregunta ahora y no al arrancar: para tipear lo que
			// el teclado no tiene, el que dicta le presta un keycode libre a un
			// keysym nuevo, y el mapa de hace un rato ya no dice la verdad.
			sym := keysymNow(x, int(ev.Detail), ev.State&xproto.ModMaskShift != 0)
			switch sym {
			case symLeft:
				if cursor > 0 {
					cursor--
				}
			case symReturn:
				insert("\n")
			case symTab:
				insert("\t")
			case symBackSpace:
				start := cursor
				if control { // Ctrl+Backspace se lleva la palabra entera
					for start > 0 && text[start-1] == ' ' {
						start--
					}
					for start > 0 && text[start-1] != ' ' {
						start--
					}
				} else if start > 0 {
					start--
				}
				text = append(text[:start], text[cursor:]...)
				cursor = start
			default:
				if r := symRune(sym); r != 0 {
					insert(string(r))
				}
			}

		case xproto.SelectionNotifyEvent:
			quiet = time.Now().Add(1500 * time.Millisecond)
			if ev.Property == 0 {
				fail("el dueño de la selección no me dio nada")
			}
			reply, err := xproto.GetProperty(x, true, win, target,
				xproto.GetPropertyTypeAny, 0, 1<<16).Reply()
			must(err)
			insert(string(reply.Value))
		}
	}
	must(os.WriteFile(outPath, []byte(string(text)), 0o644))
	fmt.Fprintf(os.Stderr, "escrito: %q\n", string(text))
}

// pollEvent no bloquea: el editor tiene que poder darse por terminado cuando
// deja de llegarle nada, y WaitForEvent lo dejaría esperando para siempre.
func pollEvent(x *xgb.Conn) (xgb.Event, xgb.Error) {
	return x.PollForEvent()
}

// keysymNow le pregunta al servidor qué produce ese keycode ahora mismo.
func keysymNow(x *xgb.Conn, code int, shift bool) xproto.Keysym {
	reply, err := xproto.GetKeyboardMapping(x, xproto.Keycode(code), 1).Reply()
	if err != nil || len(reply.Keysyms) == 0 {
		return 0
	}
	level := 0
	if shift && len(reply.Keysyms) > 1 && reply.Keysyms[1] != 0 {
		level = 1
	}
	return reply.Keysyms[level]
}

// symRune traduce un keysym a su carácter: Latin-1 va directo y el resto usa la
// convención Unicode de X.
func symRune(sym xproto.Keysym) rune {
	switch {
	case sym >= 0x20 && sym <= 0xff:
		return rune(sym)
	case sym&0x01000000 != 0:
		return rune(sym & 0x00ffffff)
	}
	return 0
}

func atom(x *xgb.Conn, name string) xproto.Atom {
	reply, err := xproto.InternAtom(x, false, uint16(len(name)), name).Reply()
	must(err)
	return reply.Atom
}

type keymapInfo struct {
	min     int
	perCode int
	syms    []xproto.Keysym
}

func loadKeymap(x *xgb.Conn, setup *xproto.SetupInfo) keymapInfo {
	min := int(setup.MinKeycode)
	count := int(setup.MaxKeycode) - min + 1
	reply, err := xproto.GetKeyboardMapping(x, xproto.Keycode(min), byte(count)).Reply()
	must(err)
	return keymapInfo{min: min, perCode: int(reply.KeysymsPerKeycode), syms: reply.Keysyms}
}

func keycodesFor(k keymapInfo, sym xproto.Keysym) map[int]bool {
	out := map[int]bool{}
	for i := 0; i+k.perCode <= len(k.syms); i += k.perCode {
		for _, s := range k.syms[i : i+k.perCode] {
			if s == sym {
				out[k.min+i/k.perCode] = true
				break
			}
		}
	}
	return out
}

func must(err error) {
	if err != nil {
		fail(err.Error())
	}
}

func fail(msg string) {
	fmt.Fprintln(os.Stderr, "receiver: "+msg)
	os.Exit(1)
}
