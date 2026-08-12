package x11

import (
	"sort"
	"sync"

	"github.com/jezek/xgb/randr"
	"github.com/jezek/xgb/xproto"
)

// En X11 las pantallas físicas no son "screens": todas viven adentro de una
// sola, pegadas en un rectángulo grande. `Screen.WidthInPixels` es el escritorio
// entero, así que centrar ahí una ventana en un setup de tres monitores la parte
// al medio, justo sobre el borde entre dos. Quién es cada pantalla lo sabe
// RandR, y de ahí salen los monitores de acá.

// Monitor es una pantalla física, con su lugar dentro del escritorio.
type Monitor struct {
	Name    string `json:"name"`
	X       int    `json:"x"`
	Y       int    `json:"y"`
	Width   int    `json:"width"`
	Height  int    `json:"height"`
	Primary bool   `json:"primary"`
}

// Contains dice si el punto cae adentro de esta pantalla.
func (m Monitor) Contains(x, y int) bool {
	return x >= m.X && x < m.X+m.Width && y >= m.Y && y < m.Y+m.Height
}

var randrOnce sync.Once

// Monitors lista las pantallas activas, de izquierda a derecha.
//
// Si RandR no está —o es tan viejo que no tiene GetMonitors— devuelve una sola
// pantalla del tamaño del escritorio, que es exactamente el comportamiento
// anterior y sirve igual en una máquina de un solo monitor.
func (c *Conn) Monitors() []Monitor {
	whole := []Monitor{{
		Name:    "todo",
		Width:   int(c.Screen.WidthInPixels),
		Height:  int(c.Screen.HeightInPixels),
		Primary: true,
	}}

	var initErr error
	randrOnce.Do(func() { initErr = randr.Init(c.X) })
	if initErr != nil {
		return whole
	}
	// GetMonitors es de RandR 1.5; preguntarle la versión primero evita el
	// pánico de xgb contra un servidor que no la tiene.
	version, err := randr.QueryVersion(c.X, 1, 5).Reply()
	if err != nil || version.MajorVersion < 1 ||
		(version.MajorVersion == 1 && version.MinorVersion < 5) {
		return whole
	}
	reply, err := randr.GetMonitors(c.X, c.Root, true).Reply()
	if err != nil || len(reply.Monitors) == 0 {
		return whole
	}

	out := make([]Monitor, 0, len(reply.Monitors))
	for _, info := range reply.Monitors {
		if info.Width == 0 || info.Height == 0 {
			continue
		}
		out = append(out, Monitor{
			Name:    c.atomName(info.Name),
			X:       int(info.X),
			Y:       int(info.Y),
			Width:   int(info.Width),
			Height:  int(info.Height),
			Primary: info.Primary,
		})
	}
	if len(out) == 0 {
		return whole
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].X != out[j].X {
			return out[i].X < out[j].X
		}
		return out[i].Y < out[j].Y
	})
	return out
}

func (c *Conn) atomName(atom xproto.Atom) string {
	reply, err := xproto.GetAtomName(c.X, atom).Reply()
	if err != nil {
		return ""
	}
	return reply.Name
}

// PointerMonitor es la pantalla donde está el mouse ahora mismo.
func (c *Conn) PointerMonitor(monitors []Monitor) (Monitor, bool) {
	reply, err := xproto.QueryPointer(c.X, c.Root).Reply()
	if err != nil {
		return Monitor{}, false
	}
	return MonitorAt(monitors, int(reply.RootX), int(reply.RootY))
}

// FocusMonitor es la pantalla donde está la ventana enfocada.
//
// Se mira el centro de la ventana y no su esquina: una ventana que arranca unos
// píxeles antes del borde igual está, para cualquiera que la mire, en la
// pantalla donde se ve.
func (c *Conn) FocusMonitor(monitors []Monitor) (Monitor, bool) {
	target := c.ActiveWindow()
	if target.Window == 0 {
		return Monitor{}, false
	}
	geometry, err := xproto.GetGeometry(c.X, xproto.Drawable(target.Window)).Reply()
	if err != nil {
		return Monitor{}, false
	}
	// La geometría viene relativa al padre: hay que llevarla a coordenadas de
	// la raíz para poder compararla con los monitores.
	absolute, err := xproto.TranslateCoordinates(c.X, target.Window, c.Root, 0, 0).Reply()
	if err != nil {
		return Monitor{}, false
	}
	x := int(absolute.DstX) + int(geometry.Width)/2
	y := int(absolute.DstY) + int(geometry.Height)/2
	return MonitorAt(monitors, x, y)
}

// PrimaryMonitor es la pantalla que el escritorio marcó como principal.
func PrimaryMonitor(monitors []Monitor) (Monitor, bool) {
	for _, m := range monitors {
		if m.Primary {
			return m, true
		}
	}
	if len(monitors) > 0 {
		return monitors[0], true
	}
	return Monitor{}, false
}

// MonitorByName busca una pantalla por su nombre de salida ("HDMI-1", "eDP-1").
func MonitorByName(monitors []Monitor, name string) (Monitor, bool) {
	for _, m := range monitors {
		if m.Name == name {
			return m, true
		}
	}
	return Monitor{}, false
}

// MonitorAt es la pantalla que contiene ese punto.
//
// Si el punto queda en el aire —pasa entre pantallas de distinto alto— gana la
// más cercana, porque devolver nada dejaría a la ventanita sin dónde aparecer.
func MonitorAt(monitors []Monitor, x, y int) (Monitor, bool) {
	if len(monitors) == 0 {
		return Monitor{}, false
	}
	for _, m := range monitors {
		if m.Contains(x, y) {
			return m, true
		}
	}
	best, bestDistance := monitors[0], -1
	for _, m := range monitors {
		distance := squaredDistance(m, x, y)
		if bestDistance < 0 || distance < bestDistance {
			best, bestDistance = m, distance
		}
	}
	return best, true
}

// squaredDistance es la distancia del punto al rectángulo, al cuadrado: alcanza
// para comparar y evita la raíz.
func squaredDistance(m Monitor, x, y int) int {
	dx := 0
	if x < m.X {
		dx = m.X - x
	} else if x >= m.X+m.Width {
		dx = x - (m.X + m.Width) + 1
	}
	dy := 0
	if y < m.Y {
		dy = m.Y - y
	} else if y >= m.Y+m.Height {
		dy = y - (m.Y + m.Height) + 1
	}
	return dx*dx + dy*dy
}
