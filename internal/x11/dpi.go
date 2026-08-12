package x11

import (
	"strconv"
	"strings"

	"github.com/jezek/xgb/xproto"
)

// Un tamaño de fuente se escribe en puntos, y un punto es 1/72 de pulgada: para
// pasarlo a píxeles hace falta saber cuántos píxeles entran en una pulgada de
// esta pantalla. Rasterizar a 72 DPI es tomar un punto por un píxel, y deja la
// letra un 25% más chica de lo que la muestra cualquier toolkit.

// DefaultDPI es lo que asumen X y los escritorios cuando nadie dice otra cosa.
const DefaultDPI = 96.0

// DPI es la resolución que declara esta sesión.
//
// Primero se mira `Xft.dpi`, que es lo que el escritorio escribe cuando lo
// cambiás a mano y lo que miran Qt y GTK. Si no está, se calcula de las
// dimensiones físicas que reporta el servidor. Y si eso da un disparate —pasa
// con monitores que mienten el tamaño— quedan los 96 de siempre.
func (c *Conn) DPI() float64 {
	if dpi, ok := c.xftDPI(); ok {
		return dpi
	}
	millimeters := float64(c.Screen.WidthInMillimeters)
	if millimeters > 0 {
		dpi := float64(c.Screen.WidthInPixels) / (millimeters / 25.4)
		if dpi >= 72 && dpi <= 400 {
			return dpi
		}
	}
	return DefaultDPI
}

// xftDPI lee Xft.dpi de los recursos X de la sesión.
func (c *Conn) xftDPI() (float64, bool) {
	reply, err := xproto.GetProperty(c.X, false, c.Root, xproto.AtomResourceManager,
		xproto.AtomString, 0, 1<<16).Reply()
	if err != nil || len(reply.Value) == 0 {
		return 0, false
	}
	for _, line := range strings.Split(string(reply.Value), "\n") {
		key, value, found := strings.Cut(line, ":")
		if !found || strings.TrimSpace(key) != "Xft.dpi" {
			continue
		}
		dpi, err := strconv.ParseFloat(strings.TrimSpace(value), 64)
		if err != nil || dpi < 72 || dpi > 400 {
			return 0, false
		}
		return dpi, true
	}
	return 0, false
}
