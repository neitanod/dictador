package x11

import (
	"fmt"
	"sync"
	"time"

	"github.com/jezek/xgb/xproto"
)

// Clipboard es dueño de las selecciones CLIPBOARD y PRIMARY.
//
// En X el que copió es el que sirve el contenido: por eso la versión Python
// dejaba un `xclip` vivo por dictado. Acá el daemon ya tiene una conexión X
// abierta y es dueño él mismo — un proceso menos por dictado, y el texto
// sobrevive mientras la app esté corriendo.
type Clipboard struct {
	conn   *Conn
	window xproto.Window

	clipboard xproto.Atom
	primary   xproto.Atom
	targets   xproto.Atom
	utf8      xproto.Atom
	text      xproto.Atom
	timestamp xproto.Atom
	timeProp  xproto.Atom

	mu    sync.RWMutex
	value string
	when  xproto.Timestamp

	// times trae la hora del servidor, que es la única que sirve para tomar la
	// selección (ver serverTime).
	times chan xproto.Timestamp

	done chan struct{}
	once sync.Once
}

// NewClipboard abre su propia conexión y una ventana invisible para recibir los
// pedidos de las otras apps.
func NewClipboard() (*Clipboard, error) {
	conn, err := Open()
	if err != nil {
		return nil, err
	}
	cb := &Clipboard{
		conn:  conn,
		done:  make(chan struct{}),
		times: make(chan xproto.Timestamp, 4),
	}

	win, err := xproto.NewWindowId(conn.X)
	if err != nil {
		conn.Close()
		return nil, err
	}
	// InputOnly y de 1×1: nunca se dibuja, sólo existe para tener una dirección
	// a la que los otros clientes le pidan el contenido de la selección.
	const depthFromParent = 0
	err = xproto.CreateWindowChecked(conn.X, depthFromParent,
		win, conn.Root, 0, 0, 1, 1, 0,
		xproto.WindowClassInputOnly, conn.Screen.RootVisual,
		xproto.CwEventMask, []uint32{xproto.EventMaskPropertyChange}).Check()
	if err != nil {
		conn.Close()
		return nil, err
	}
	cb.window = win

	for _, pair := range []struct {
		name string
		dst  *xproto.Atom
	}{
		{"CLIPBOARD", &cb.clipboard},
		{"TARGETS", &cb.targets},
		{"UTF8_STRING", &cb.utf8},
		{"TEXT", &cb.text},
		{"TIMESTAMP", &cb.timestamp},
		{"DICTADOR_TIMESTAMP", &cb.timeProp},
	} {
		atom, err := conn.Atom(pair.name)
		if err != nil {
			conn.Close()
			return nil, err
		}
		*pair.dst = atom
	}
	cb.primary = xproto.AtomPrimary

	go cb.serve()
	return cb, nil
}

// serverTime le pide la hora al servidor X.
//
// SetSelectionOwner ignora en silencio cualquier timestamp posterior al reloj
// del servidor, así que un time.Now() —que cuenta desde 1970 y el servidor
// desde que arrancó— hace que la selección nunca se tome y el portapapeles
// quede vacío sin un solo error. La forma canónica de conseguir la hora buena
// es agregarle cero bytes a una propiedad nuestra: el servidor contesta con un
// PropertyNotify que trae SU reloj.
func (cb *Clipboard) serverTime() xproto.Timestamp {
	// Vaciar lo que haya quedado de una vez anterior.
	for {
		select {
		case <-cb.times:
			continue
		default:
		}
		break
	}
	err := xproto.ChangePropertyChecked(cb.conn.X, xproto.PropModeAppend, cb.window,
		cb.timeProp, xproto.AtomString, 8, 0, nil).Check()
	if err != nil {
		return xproto.TimeCurrentTime
	}
	select {
	case t := <-cb.times:
		return t
	case <-time.After(500 * time.Millisecond):
		return xproto.TimeCurrentTime
	}
}

// Set copia el texto y toma la propiedad de las dos selecciones.
func (cb *Clipboard) Set(text string) error {
	now := cb.serverTime()
	cb.mu.Lock()
	cb.value = text
	cb.when = now
	cb.mu.Unlock()

	for _, sel := range []xproto.Atom{cb.clipboard, cb.primary} {
		if err := xproto.SetSelectionOwnerChecked(cb.conn.X, cb.window, sel, now).Check(); err != nil {
			return err
		}
		// El servidor puede ignorar el pedido sin devolver error, así que la
		// única confirmación real es preguntar quién quedó de dueño.
		reply, err := xproto.GetSelectionOwner(cb.conn.X, sel).Reply()
		if err != nil {
			return err
		}
		if reply.Owner != cb.window {
			return fmt.Errorf("el servidor X no me dio la selección")
		}
	}
	return nil
}

// Text es lo último que se copió desde acá.
func (cb *Clipboard) Text() string {
	cb.mu.RLock()
	defer cb.mu.RUnlock()
	return cb.value
}

// Owns dice si todavía somos dueños del portapapeles.
func (cb *Clipboard) Owns() bool {
	reply, err := xproto.GetSelectionOwner(cb.conn.X, cb.clipboard).Reply()
	return err == nil && reply.Owner == cb.window
}

// Close suelta la selección y cierra la conexión.
func (cb *Clipboard) Close() {
	cb.once.Do(func() {
		close(cb.done)
		cb.conn.Close()
	})
}

// serve atiende los pedidos de las apps que quieren pegar lo que copiamos.
func (cb *Clipboard) serve() {
	for {
		event, xerr := cb.conn.X.WaitForEvent()
		select {
		case <-cb.done:
			return
		default:
		}
		if xerr != nil {
			continue
		}
		if event == nil {
			return
		}
		switch ev := event.(type) {
		case xproto.SelectionRequestEvent:
			cb.answer(ev)
		case xproto.PropertyNotifyEvent:
			if ev.Atom == cb.timeProp {
				select {
				case cb.times <- ev.Time:
				default:
				}
			}
		case xproto.SelectionClearEvent:
			// Otro se quedó con el portapapeles: nada que hacer, es lo normal
			// cuando el usuario copia algo en otro lado.
		}
	}
}

func (cb *Clipboard) answer(req xproto.SelectionRequestEvent) {
	property := req.Property
	if property == 0 { // clientes viejos: la convención dice usar el target
		property = req.Target
	}

	cb.mu.RLock()
	value, when := cb.value, cb.when
	cb.mu.RUnlock()

	ok := true
	switch req.Target {
	case cb.targets:
		list := []xproto.Atom{cb.targets, cb.timestamp, cb.utf8, xproto.AtomString, cb.text}
		data := make([]byte, 0, len(list)*4)
		for _, atom := range list {
			data = append(data, byte(atom), byte(atom>>8), byte(atom>>16), byte(atom>>24))
		}
		ok = cb.setProperty(req.Requestor, property, xproto.AtomAtom, 32, data) == nil
	case cb.timestamp:
		data := []byte{byte(when), byte(when >> 8), byte(when >> 16), byte(when >> 24)}
		ok = cb.setProperty(req.Requestor, property, xproto.AtomInteger, 32, data) == nil
	case cb.utf8, cb.text:
		ok = cb.setProperty(req.Requestor, property, cb.utf8, 8, []byte(value)) == nil
	case xproto.AtomString:
		ok = cb.setProperty(req.Requestor, property, xproto.AtomString, 8, []byte(value)) == nil
	default:
		ok = false
	}
	if !ok {
		property = 0 // "no te lo puedo dar", que es la forma de decir que no
	}

	notify := xproto.SelectionNotifyEvent{
		Time:      req.Time,
		Requestor: req.Requestor,
		Selection: req.Selection,
		Target:    req.Target,
		Property:  property,
	}
	_ = xproto.SendEventChecked(cb.conn.X, false, req.Requestor, 0,
		string(notify.Bytes())).Check()
}

func (cb *Clipboard) setProperty(win xproto.Window, prop, typ xproto.Atom, format byte, data []byte) error {
	return xproto.ChangePropertyChecked(cb.conn.X, xproto.PropModeReplace, win,
		prop, typ, format, uint32(len(data)/int(format/8)), data).Check()
}
