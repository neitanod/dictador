// Comando receiver: una ventana que recibe el pegado, para el test end-to-end.
//
// Es el equivalente del campo de texto al que le dictás: se abre, toma el foco
// y espera un Ctrl+V (o Ctrl+Shift+V, como manda en las terminales). Cuando lo
// recibe pide el contenido del portapapeles y lo escribe en un archivo, que es
// lo que el test lee para saber si el texto hizo el camino completo.
//
//	receiver <archivo de salida> [clase de ventana]
package main

import (
	"fmt"
	"os"
	"time"

	"github.com/jezek/xgb"
	"github.com/jezek/xgb/xproto"
)

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "uso: receiver <archivo> [clase]")
		os.Exit(2)
	}
	outPath := os.Args[1]
	class := "dictador-receiver"
	if len(os.Args) > 2 {
		class = os.Args[2]
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
