package overlay

import (
	"image"
	"strings"

	"github.com/neitanod/dictador/internal/x11"
)

// Dónde aparece la ventanita se decide en dos pasos: en qué pantalla, y en qué
// lugar de esa pantalla.

// Screens son las formas de elegir pantalla, con la etiqueta que se muestra.
var Screens = []struct {
	Value string
	Label string
}{
	{"mouse", "donde está el mouse"},
	{"focus", "donde está la ventana que estás usando"},
	{"primary", "siempre en la pantalla principal"},
	{"all", "en todas las pantallas a la vez"},
}

// Positions son los siete lugares donde puede aparecer.
var Positions = []struct {
	Value string
	Label string
}{
	{"top-left", "arriba a la izquierda"},
	{"top-center", "arriba al centro"},
	{"top-right", "arriba a la derecha"},
	{"center", "al medio"},
	{"bottom-left", "abajo a la izquierda"},
	{"bottom-center", "abajo al centro"},
	{"bottom-right", "abajo a la derecha"},
}

// ValidPosition dice si ese lugar existe.
func ValidPosition(position string) bool {
	for _, p := range Positions {
		if p.Value == position {
			return true
		}
	}
	return false
}

// targets elige en qué pantallas hay que dibujar.
//
// Cualquier valor que no sea una de las formas conocidas se toma como el nombre
// de una salida ("HDMI-1", "eDP-1"), que es como se pide "siempre en esa". Si
// esa pantalla no está conectada ahora, se cae a la del mouse en vez de no
// mostrar nada.
func targets(conn *x11.Conn, screen string) []x11.Monitor {
	monitors := conn.Monitors()
	if len(monitors) == 0 {
		return nil
	}
	switch strings.TrimSpace(screen) {
	case "all":
		return monitors
	case "primary":
		if m, ok := x11.PrimaryMonitor(monitors); ok {
			return []x11.Monitor{m}
		}
	case "focus":
		if m, ok := conn.FocusMonitor(monitors); ok {
			return []x11.Monitor{m}
		}
		// Sin ventana enfocada —o sin poder ubicarla— el mouse es la mejor
		// pista que queda de dónde estás mirando.
		if m, ok := conn.PointerMonitor(monitors); ok {
			return []x11.Monitor{m}
		}
	case "", "mouse":
		if m, ok := conn.PointerMonitor(monitors); ok {
			return []x11.Monitor{m}
		}
	default:
		if m, ok := x11.MonitorByName(monitors, strings.TrimSpace(screen)); ok {
			return []x11.Monitor{m}
		}
		if m, ok := conn.PointerMonitor(monitors); ok {
			return []x11.Monitor{m}
		}
	}
	if m, ok := x11.PrimaryMonitor(monitors); ok {
		return []x11.Monitor{m}
	}
	return monitors[:1]
}

// place calcula en qué coordenadas va la ventanita adentro de una pantalla.
func place(monitor x11.Monitor, size image.Point, position string, margin int) (int, int) {
	if margin < 0 {
		margin = 0
	}
	left := monitor.X + margin
	right := monitor.X + monitor.Width - size.X - margin
	centerX := monitor.X + (monitor.Width-size.X)/2
	top := monitor.Y + margin
	bottom := monitor.Y + monitor.Height - size.Y - margin
	centerY := monitor.Y + (monitor.Height-size.Y)/2

	var x, y int
	switch position {
	case "top-left":
		x, y = left, top
	case "top-center":
		x, y = centerX, top
	case "top-right":
		x, y = right, top
	case "center":
		x, y = centerX, centerY
	case "bottom-left":
		x, y = left, bottom
	case "bottom-right":
		x, y = right, bottom
	default: // bottom-center, y cualquier cosa rara
		x, y = centerX, bottom
	}

	// Una ventanita más ancha que la pantalla no puede quedar afuera: mejor
	// pegada al borde y cortada que invisible.
	if x < monitor.X {
		x = monitor.X
	}
	if y < monitor.Y {
		y = monitor.Y
	}
	return x, y
}
