package x11

import (
	"encoding/binary"
	"fmt"
	"sort"
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
	// Cancel: se apretó Escape, que corta lo que esté en curso.
	Cancel
)

// Listener escucha la tecla del dictado con su propia conexión X.
type Listener struct {
	conn   *Conn
	combo  *Combo
	keymap *Keymap
	events chan HotkeyEvent
	done   chan struct{}
	once   sync.Once

	triggerDown bool
	engaged     bool
	escape      map[int]bool

	// Las teclas de idioma se leen desde el bucle de eventos y se cambian desde
	// el daemon cuando se guarda la configuración, así que van con candado.
	mu      sync.Mutex
	langs   *LanguageKeys
	tracker *langTracker
	// release es la tecla de idioma que estaba en juego al soltar el combo, que
	// es la que decide el idioma del dictado que se acaba de terminar.
	release LanguageKey
	grabbed []int
	hints   chan LanguageKey
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
	// Escape se resuelve una vez: es la tecla de arrepentirse, y tiene que
	// funcionar aunque la traducción esté apagada.
	escape, err := resolve(keymap, "Escape")
	if err != nil {
		escape = map[int]bool{}
	}
	tracker := newLangTracker()
	tracker.keys = &LanguageKeys{codes: map[int]LanguageKey{}}
	return &Listener{
		conn:    conn,
		combo:   combo,
		escape:  escape,
		keymap:  keymap,
		events:  make(chan HotkeyEvent, 8),
		done:    make(chan struct{}),
		tracker: tracker,
		langs:   tracker.keys,
		hints:   make(chan LanguageKey, 8),
	}, nil
}

// WatchLanguages le dice al listener qué letras eligen idioma.
//
// Se puede llamar con el dictado andando: es lo que pasa cuando guardás la
// tabla desde la pantalla de configuración.
func (l *Listener) WatchLanguages(table map[string]string) []error {
	keys, problems := NewLanguageKeys(table, l.keymap)
	l.mu.Lock()
	l.langs = keys
	l.tracker.keys = keys
	l.tracker.reset()
	l.mu.Unlock()
	return problems
}

// Hints avisa a qué idioma se va a traducir mientras seguís hablando, para que
// la ventanita lo muestre antes de que sueltes.
func (l *Listener) Hints() <-chan LanguageKey { return l.hints }

// ReleaseLanguage es el idioma que estaba elegido cuando soltaste el combo.
//
// Se lee después de recibir el Release, y el canal es lo que garantiza que lo
// que se lee es lo que se escribió antes de mandarlo.
func (l *Listener) ReleaseLanguage() LanguageKey {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.release
}

// GrabLanguageKeys se queda con las teclas de idioma mientras dura la
// grabación, para que elegir el idioma no escriba la letra en la app de
// enfrente.
func (l *Listener) GrabLanguageKeys() error {
	l.mu.Lock()
	codes := l.langs.Keycodes()
	if len(l.grabbed) > 0 || len(codes) == 0 {
		l.mu.Unlock()
		return nil
	}
	l.grabbed = codes
	l.mu.Unlock()
	return l.conn.GrabKeys(codes)
}

// GrabCancelKey se queda con Escape mientras dura la pausa de la traducción.
//
// Sin el grab, el Escape con el que cancelás el pegado le llega también a la
// aplicación de adelante, y ahí cierra el diálogo o el menú que tuvieras
// abierto: cancelar una cosa terminaría cancelando dos.
func (l *Listener) GrabCancelKey() error {
	return l.conn.GrabKeys(l.escapeCodes())
}

// UngrabCancelKey devuelve Escape al resto del sistema.
func (l *Listener) UngrabCancelKey() { l.conn.UngrabKeys(l.escapeCodes()) }

func (l *Listener) escapeCodes() []int {
	codes := make([]int, 0, len(l.escape))
	for code := range l.escape {
		codes = append(codes, code)
	}
	sort.Ints(codes)
	return codes
}

// UngrabLanguageKeys devuelve las teclas al resto del sistema.
func (l *Listener) UngrabLanguageKeys() {
	l.mu.Lock()
	codes := l.grabbed
	l.grabbed = nil
	l.mu.Unlock()
	if len(codes) > 0 {
		l.conn.UngrabKeys(codes)
	}
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
	isLanguage := !isTrigger && !isMod && l.trackLanguage(raw)

	if raw.Evtype == xiRawKeyPress {
		switch {
		case l.escape[raw.Keycode]:
			// Escape es arrepentirse: corta el dictado en curso, y durante la
			// pausa de la traducción cancela el pegado.
			l.emit(Cancel)
		case isLanguage:
			// Elegir el idioma no cancela el dictado ni cuenta como "otra
			// tecla": es parte de dictar.
		case isTrigger:
			l.triggerDown = true
			// X repite el press mientras la tecla está hundida.
			if !l.engaged && l.modsHeld() {
				l.engaged = true
				l.startLanguages()
				l.emit(Press)
			}
		case isMod:
			// Completar el combo al revés (primero el gatillo, después el
			// modificador) también tiene que valer.
			if !l.engaged && l.triggerDown && l.modsHeld() {
				l.engaged = true
				l.startLanguages()
				l.emit(Press)
			}
		default:
			l.emit(OtherKey)
		}
		return
	}

	switch {
	case isLanguage:
	case isTrigger:
		l.triggerDown = false
		if l.engaged {
			l.engaged = false
			l.rememberLanguage()
			l.emit(Release)
		}
	case isMod && l.engaged && !l.modsHeld():
		l.engaged = false
		l.rememberLanguage()
		l.emit(Release)
	}
}

// trackLanguage anota el press o el release de una tecla de idioma y avisa a la
// ventanita si el idioma elegido cambió. Devuelve si la tecla era de idioma.
func (l *Listener) trackLanguage(raw RawKeyEvent) bool {
	l.mu.Lock()
	if _, ok := l.langs.Lookup(raw.Keycode); !ok {
		l.mu.Unlock()
		return false
	}
	before := l.tracker.live()
	if raw.Evtype == xiRawKeyPress {
		l.tracker.press(raw.Keycode)
	} else {
		l.tracker.release(raw.Keycode)
	}
	after := l.tracker.live()
	engaged := l.engaged
	l.mu.Unlock()

	if engaged && after != before {
		select {
		case l.hints <- after:
		default:
		}
	}
	return true
}

// rememberLanguage congela el idioma en el instante del release, que es cuando
// se decide, y no cuando el daemon llega a mirarlo.
func (l *Listener) rememberLanguage() {
	l.mu.Lock()
	empty := l.tracker.keys.Empty()
	l.mu.Unlock()
	if empty {
		l.mu.Lock()
		l.release = LanguageKey{}
		l.mu.Unlock()
		return
	}
	// Preguntarle a X qué sigue hundido es lo que salva al release que se
	// perdió; va afuera del candado porque es un viaje al servidor.
	confirmed, err := l.conn.KeysDown()
	if err != nil {
		confirmed = nil
	}
	l.mu.Lock()
	key, ok := l.tracker.current(confirmed)
	if !ok {
		key = LanguageKey{}
	}
	l.release = key
	l.mu.Unlock()
}

// startLanguages arranca un dictado con la cuenta de teclas de idioma en cero:
// lo que hayas apretado antes de empezar a hablar no elige nada.
func (l *Listener) startLanguages() {
	l.mu.Lock()
	l.tracker.reset()
	l.release = LanguageKey{}
	l.mu.Unlock()
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
