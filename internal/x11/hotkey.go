package x11

import (
	"encoding/binary"
	"fmt"
	"sync"

	"github.com/jezek/xgb"
	"github.com/jezek/xgb/xproto"
)

// La tecla se escucha por XInput2 raw sobre la root window, sin hacer grab: la
// tecla sigue funcionando normalmente para el resto del sistema y nosotros nos
// enteramos igual. Los atajos globales del escritorio no sirven acá porque
// avisan sólo del press, y el push-to-talk necesita el release.
//
// xgb no trae la extensión XInput, así que las dos peticiones que hacen falta
// se arman byte a byte. Son cortas y el protocolo está congelado desde 2009.
const (
	xgeEventType = 35 // GenericEvent: por acá llegan todos los XI2

	xiQueryVersion  = 47
	xiSelectEvents  = 46
	xiAllMasterDevs = 1

	xiRawKeyPress   = 13
	xiRawKeyRelease = 14
)

// RawKeyEvent es un press o release en crudo, ya desarmado.
type RawKeyEvent struct {
	Evtype  uint16
	Keycode int
	raw     []byte
}

func (e RawKeyEvent) Bytes() []byte { return e.raw }
func (e RawKeyEvent) String() string {
	kind := "press"
	if e.Evtype == xiRawKeyRelease {
		kind = "release"
	}
	return fmt.Sprintf("XIRawKey%s{keycode: %d}", kind, e.Keycode)
}

// registerRawEvents le enseña a xgb a devolver los GenericEvent sin masticar.
//
// Los XI2 raw no vienen con un parser en ninguna librería de Go, así que el
// constructor se queda con lo único que nos importa: qué tipo de evento es y
// qué keycode lo produjo (offset 16 del xXIRawEvent).
var registerRawEvents = sync.OnceFunc(func() {
	xgb.NewEventFuncs[xgeEventType] = func(buf []byte) xgb.Event {
		if len(buf) < 32 {
			return RawKeyEvent{raw: buf}
		}
		return RawKeyEvent{
			Evtype:  binary.LittleEndian.Uint16(buf[8:]),
			Keycode: int(binary.LittleEndian.Uint32(buf[16:])),
			raw:     buf,
		}
	}
})

// EnableXInput negocia XInput 2 y devuelve el opcode mayor de la extensión.
func (c *Conn) EnableXInput() (byte, error) {
	reply, err := xproto.QueryExtension(c.X, uint16(len("XInputExtension")), "XInputExtension").Reply()
	if err != nil {
		return 0, fmt.Errorf("no pude preguntar por XInput: %w", err)
	}
	if !reply.Present {
		return 0, fmt.Errorf("el servidor X no tiene la extensión XInput")
	}
	opcode := reply.MajorOpcode

	// XIQueryVersion: además de decirnos qué hay, le declara al servidor qué
	// versión hablamos. Sin esto los eventos raw no se entregan.
	buf := make([]byte, 8)
	buf[0] = opcode
	buf[1] = xiQueryVersion
	binary.LittleEndian.PutUint16(buf[2:], 2) // largo en unidades de 4 bytes
	binary.LittleEndian.PutUint16(buf[4:], 2) // major que pedimos
	binary.LittleEndian.PutUint16(buf[6:], 0) // minor
	cookie := c.X.NewCookie(true, true)
	c.X.NewRequest(buf, cookie)
	raw, err := cookie.Reply()
	if err != nil {
		return 0, fmt.Errorf("XIQueryVersion falló: %w", err)
	}
	major := binary.LittleEndian.Uint16(raw[8:])
	minor := binary.LittleEndian.Uint16(raw[10:])
	if major < 2 {
		return 0, fmt.Errorf("hace falta XInput 2.0, hay %d.%d", major, minor)
	}
	registerRawEvents()
	return opcode, nil
}

// selectRawKeys pide los press y release en crudo de todos los teclados master.
func (c *Conn) selectRawKeys(opcode byte, window xproto.Window) error {
	const maskLen = 1 // 4 bytes alcanzan: los bits que queremos son el 13 y el 14
	buf := make([]byte, 20)
	buf[0] = opcode
	buf[1] = xiSelectEvents
	binary.LittleEndian.PutUint16(buf[2:], 5) // 20 bytes / 4
	binary.LittleEndian.PutUint32(buf[4:], uint32(window))
	binary.LittleEndian.PutUint16(buf[8:], 1) // un solo XIEventMask
	// buf[10:12] es padding
	binary.LittleEndian.PutUint16(buf[12:], xiAllMasterDevs)
	binary.LittleEndian.PutUint16(buf[14:], maskLen)
	binary.LittleEndian.PutUint32(buf[16:], 1<<xiRawKeyPress|1<<xiRawKeyRelease)

	cookie := c.X.NewCookie(true, false)
	c.X.NewRequest(buf, cookie)
	if err := cookie.Check(); err != nil {
		return fmt.Errorf("XISelectEvents falló: %w", err)
	}
	return nil
}

// HotkeyEvent es lo que el listener le cuenta al daemon.
type HotkeyEvent int

const (
	// Press: se completó el combo (la tecla, con sus modificadores puestos).
	Press HotkeyEvent = iota
	// Release: se soltó el gatillo o se cayó un modificador.
	Release
	// OtherKey: se apretó cualquier otra tecla, que es motivo para cancelar.
	OtherKey
)

// Listener escucha la tecla del dictado con su propia conexión X.
type Listener struct {
	conn   *Conn
	combo  *Combo
	events chan HotkeyEvent
	done   chan struct{}
	once   sync.Once

	triggerDown bool
	engaged     bool
}

// NewListener abre la conexión, valida la tecla y deja todo listo para Run.
//
// Se hace en dos pasos a propósito: así un hotkey mal escrito falla en el
// arranque, con un mensaje, en vez de en un goroutine que nadie mira.
func NewListener(keySpec string) (*Listener, error) {
	conn, err := Open()
	if err != nil {
		return nil, err
	}
	opcode, err := conn.EnableXInput()
	if err != nil {
		conn.Close()
		return nil, err
	}
	keymap, err := conn.LoadKeymap()
	if err != nil {
		conn.Close()
		return nil, err
	}
	combo, err := ParseCombo(keySpec, keymap)
	if err != nil {
		conn.Close()
		return nil, err
	}
	if err := conn.selectRawKeys(opcode, conn.Root); err != nil {
		conn.Close()
		return nil, err
	}
	return &Listener{
		conn:   conn,
		combo:  combo,
		events: make(chan HotkeyEvent, 8),
		done:   make(chan struct{}),
	}, nil
}

// Combo es la tecla que quedó escuchando.
func (l *Listener) Combo() *Combo { return l.combo }

// Events es por donde salen los press, release y teclas ajenas.
func (l *Listener) Events() <-chan HotkeyEvent { return l.events }

// Run bloquea leyendo eventos hasta que se llame a Stop.
func (l *Listener) Run() {
	defer close(l.events)
	for {
		event, xerr := l.conn.X.WaitForEvent()
		select {
		case <-l.done:
			return
		default:
		}
		if xerr != nil {
			continue
		}
		if event == nil { // la conexión se cerró
			return
		}
		raw, ok := event.(RawKeyEvent)
		if !ok || (raw.Evtype != xiRawKeyPress && raw.Evtype != xiRawKeyRelease) {
			continue
		}
		l.handle(raw)
	}
}

func (l *Listener) handle(raw RawKeyEvent) {
	isTrigger := l.combo.Trigger[raw.Keycode]
	isMod := !isTrigger && l.combo.All[raw.Keycode]

	if raw.Evtype == xiRawKeyPress {
		switch {
		case isTrigger:
			l.triggerDown = true
			// X repite el press mientras la tecla está hundida.
			if !l.engaged && l.modsHeld() {
				l.engaged = true
				l.emit(Press)
			}
		case isMod:
			// Completar el combo al revés (primero el gatillo, después el
			// modificador) también tiene que valer.
			if !l.engaged && l.triggerDown && l.modsHeld() {
				l.engaged = true
				l.emit(Press)
			}
		default:
			l.emit(OtherKey)
		}
		return
	}

	switch {
	case isTrigger:
		l.triggerDown = false
		if l.engaged {
			l.engaged = false
			l.emit(Release)
		}
	case isMod && l.engaged && !l.modsHeld():
		l.engaged = false
		l.emit(Release)
	}
}

func (l *Listener) modsHeld() bool {
	if len(l.combo.Mods) == 0 {
		return true
	}
	down, err := l.conn.KeysDown()
	if err != nil {
		return false
	}
	return l.combo.held(down)
}

func (l *Listener) emit(e HotkeyEvent) {
	select {
	case l.events <- e:
	case <-l.done:
	}
}

// Stop corta el listener y suelta la conexión.
func (l *Listener) Stop() {
	l.once.Do(func() {
		close(l.done)
		// Cerrar la conexión es lo que despierta al WaitForEvent bloqueado.
		l.conn.Close()
	})
}
