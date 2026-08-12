// Package x11 es todo lo que la app le pide al servidor X: escuchar la tecla
// sin agarrarla, saber en qué ventana estabas, ser dueño del portapapeles y
// pegar el texto.
//
// Reemplaza a xdotool, xclip y xprop, que en la versión Python eran tres
// binarios externos y un proceso zombi por dictado.
package x11

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"strconv"
	"strings"
	"sync/atomic"

	"github.com/jezek/xgb"
	"github.com/jezek/xgb/xproto"
)

// Conn es una conexión al servidor X con la información que usamos seguido.
type Conn struct {
	X      *xgb.Conn
	Setup  *xproto.SetupInfo
	Screen *xproto.ScreenInfo
	Root   xproto.Window
}

// Open abre una conexión al display de la variable DISPLAY.
func Open() (*Conn, error) {
	display := os.Getenv("DISPLAY")
	if display == "" {
		return nil, errors.New("no hay DISPLAY: ¿estás en una sesión gráfica X11?")
	}
	_, num, _, err := parseDisplay(display)
	if err != nil {
		return nil, err
	}
	netConn, err := dial(display)
	if err != nil {
		return nil, err
	}
	filter := &genericEventFilter{Conn: netConn}
	// Con cookie si hay: ver auth.go para por qué no alcanza con dejársela a xgb.
	var x *xgb.Conn
	if cookie := authCookie(strconv.Itoa(num)); cookie != "" {
		x, err = xgb.NewConnNetWithCookieHex(filter, cookie)
	} else {
		x, err = xgb.NewConnNet(filter)
	}
	if err != nil {
		_ = netConn.Close()
		return nil, fmt.Errorf("no pude conectarme a %s: %w", display, err)
	}
	// Recién ahora, con el saludo ya negociado, el filtro empieza a reencuadrar.
	filter.active.Store(true)

	setup := xproto.Setup(x)
	screen := setup.DefaultScreen(x)
	return &Conn{X: x, Setup: setup, Screen: screen, Root: screen.Root}, nil
}

// Close suelta la conexión.
func (c *Conn) Close() {
	if c != nil && c.X != nil {
		c.X.Close()
	}
}

// dial abre el socket del display sin pasar por xgb, para poder envolverlo.
func dial(display string) (net.Conn, error) {
	host, num, _, err := parseDisplay(display)
	if err != nil {
		return nil, err
	}
	if host == "" || host == "unix" {
		return net.Dial("unix", "/tmp/.X11-unix/X"+strconv.Itoa(num))
	}
	return net.Dial("tcp", fmt.Sprintf("%s:%d", host, 6000+num))
}

// parseDisplay parte "host:display.screen" en sus tres pedazos.
func parseDisplay(display string) (host string, num, screen int, err error) {
	colon := strings.LastIndex(display, ":")
	if colon < 0 {
		return "", 0, 0, fmt.Errorf("DISPLAY inválido: %q", display)
	}
	host = display[:colon]
	rest := display[colon+1:]
	if dot := strings.Index(rest, "."); dot >= 0 {
		screen, _ = strconv.Atoi(rest[dot+1:])
		rest = rest[:dot]
	}
	num, err = strconv.Atoi(rest)
	if err != nil {
		return "", 0, 0, fmt.Errorf("DISPLAY inválido: %q", display)
	}
	return host, num, screen, nil
}

// genericEventFilter reencuadra los mensajes de X antes de que xgb los lea.
//
// El read loop de xgb lee los eventos de a 32 bytes fijos y no consume el
// payload extra que un GenericEvent (tipo 35) puede traer detrás. Para un
// teclado ese extra es cero —los teclados no tienen valuadores— pero el día que
// no lo sea no se pierde un evento: se desincroniza el socket y la conexión
// entera queda basura. Así que acá se lee el mensaje completo y se descarta lo
// que xgb no va a pedir.
type genericEventFilter struct {
	net.Conn
	active  atomic.Bool
	pending []byte
}

func (f *genericEventFilter) Read(p []byte) (int, error) {
	if !f.active.Load() {
		return f.Conn.Read(p)
	}
	for len(f.pending) == 0 {
		hdr := make([]byte, 32)
		if _, err := io.ReadFull(f.Conn, hdr); err != nil {
			return 0, err
		}
		switch {
		case hdr[0] == 1: // una respuesta: el largo extra lo pide xgb después
			extra := int(binary.LittleEndian.Uint32(hdr[4:])) * 4
			f.pending = hdr
			if extra > 0 {
				buf := make([]byte, extra)
				if _, err := io.ReadFull(f.Conn, buf); err != nil {
					return 0, err
				}
				f.pending = append(f.pending, buf...)
			}
		case hdr[0]&0x7f == xgeEventType: // GenericEvent: el extra lo tiramos
			if extra := int(binary.LittleEndian.Uint32(hdr[4:])) * 4; extra > 0 {
				if _, err := io.CopyN(io.Discard, f.Conn, int64(extra)); err != nil {
					return 0, err
				}
			}
			f.pending = hdr
		default: // error o evento común: 32 bytes y nada más
			f.pending = hdr
		}
	}
	n := copy(p, f.pending)
	f.pending = f.pending[n:]
	return n, nil
}

// ARGBVisual busca un visual de 32 bits, que es lo que permite una ventana con
// transparencia real. Sin uno, el overlay no se puede dibujar translúcido.
func (c *Conn) ARGBVisual() (xproto.Visualid, byte, bool) {
	for _, depth := range c.Screen.AllowedDepths {
		if depth.Depth != 32 {
			continue
		}
		for _, visual := range depth.Visuals {
			if visual.Class == xproto.VisualClassTrueColor {
				return visual.VisualId, 32, true
			}
		}
	}
	return 0, 0, false
}
