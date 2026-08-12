package overlay

import (
	"image"
	"testing"

	"github.com/neitanod/dictador/internal/x11"
)

// El escritorio de prueba tiene la forma del de Sebastián: tres pantallas
// pegadas, de distinto alto y a distinta altura. Centrar en el rectángulo que
// las contiene a todas cae justo sobre un borde, que es el bug que esto arregla.
var laptop = x11.Monitor{Name: "eDP-1", X: 240, Y: 1080, Width: 1680, Height: 1050}
var tele = x11.Monitor{Name: "HDMI-1-0", X: 0, Y: 0, Width: 1920, Height: 1080, Primary: true}
var monitor3 = x11.Monitor{Name: "DP-1-2", X: 1920, Y: 670, Width: 1680, Height: 1050}

func todas() []x11.Monitor { return []x11.Monitor{tele, laptop, monitor3} }

func TestPlacePoneLaVentanitaEnLosSieteLugares(t *testing.T) {
	size := image.Pt(780, 120)
	const margin = 90
	cases := []struct {
		position string
		x, y     int
	}{
		{"top-left", 240 + 90, 1080 + 90},
		{"top-center", 240 + (1680-780)/2, 1080 + 90},
		{"top-right", 240 + 1680 - 780 - 90, 1080 + 90},
		{"center", 240 + (1680-780)/2, 1080 + (1050-120)/2},
		{"bottom-left", 240 + 90, 1080 + 1050 - 120 - 90},
		{"bottom-center", 240 + (1680-780)/2, 1080 + 1050 - 120 - 90},
		{"bottom-right", 240 + 1680 - 780 - 90, 1080 + 1050 - 120 - 90},
	}
	for _, c := range cases {
		x, y := place(laptop, size, c.position, margin)
		if x != c.x || y != c.y {
			t.Errorf("%s → (%d, %d), quería (%d, %d)", c.position, x, y, c.x, c.y)
		}
	}
}

func TestPlaceCaeEnAbajoAlCentroSiNoEntiendeElLugar(t *testing.T) {
	size := image.Pt(780, 120)
	want, wantY := place(laptop, size, "bottom-center", 90)
	got, gotY := place(laptop, size, "en el techo", 90)
	if got != want || gotY != wantY {
		t.Errorf("(%d, %d), quería (%d, %d)", got, gotY, want, wantY)
	}
}

func TestPlaceQuedaSiempreDentroDeSuPantalla(t *testing.T) {
	// Una ventanita más ancha que la pantalla, o un margen absurdo: pegada al
	// borde de SU pantalla, nunca invadiendo la de al lado.
	for _, position := range []string{"top-left", "bottom-right", "center"} {
		x, y := place(laptop, image.Pt(3000, 2000), position, 500)
		if x < laptop.X || y < laptop.Y {
			t.Errorf("%s se fue de la pantalla: (%d, %d)", position, x, y)
		}
	}
}

func TestPlaceRespetaLaPantallaQueLeToca(t *testing.T) {
	size := image.Pt(780, 120)
	x, _ := place(monitor3, size, "bottom-center", 90)
	if x < monitor3.X || x+size.X > monitor3.X+monitor3.Width {
		t.Errorf("x = %d, se sale de DP-1-2", x)
	}
	// Y la del medio no es la misma que la de la izquierda: el bug era
	// justamente que todas terminaban en el mismo lado.
	other, _ := place(tele, size, "bottom-center", 90)
	if x == other {
		t.Error("dos pantallas distintas dieron la misma posición")
	}
}

func TestValidPositionConoceLasSiete(t *testing.T) {
	if len(Positions) != 7 {
		t.Errorf("son siete lugares, hay %d", len(Positions))
	}
	for _, p := range Positions {
		if !ValidPosition(p.Value) {
			t.Errorf("%q tendría que valer", p.Value)
		}
		if p.Label == "" {
			t.Errorf("%q no tiene cómo mostrarse", p.Value)
		}
	}
	if ValidPosition("al costado") {
		t.Error("un lugar inventado no tendría que valer")
	}
}

func TestMonitorAtEncuentraLaPantallaDelPunto(t *testing.T) {
	m, ok := x11.MonitorAt(todas(), 1000, 1500)
	if !ok || m.Name != "eDP-1" {
		t.Errorf("dio %q", m.Name)
	}
	m, _ = x11.MonitorAt(todas(), 100, 100)
	if m.Name != "HDMI-1-0" {
		t.Errorf("dio %q", m.Name)
	}
	m, _ = x11.MonitorAt(todas(), 2500, 700)
	if m.Name != "DP-1-2" {
		t.Errorf("dio %q", m.Name)
	}
}

func TestMonitorAtCaeEnLaMasCercanaSiElPuntoQuedaEnElAire(t *testing.T) {
	// Entre la tele (termina en y=1080, x<1920) y la de la derecha (arranca en
	// y=670) queda un hueco: arriba a la izquierda de la laptop, x<240.
	m, ok := x11.MonitorAt(todas(), 50, 1500)
	if !ok {
		t.Fatal("siempre tiene que devolver alguna: sin pantalla no hay dónde dibujar")
	}
	if m.Name != "eDP-1" {
		t.Errorf("la más cercana a (50, 1500) es la laptop, dio %q", m.Name)
	}
}

func TestMonitorAtSinPantallasNoInventa(t *testing.T) {
	if _, ok := x11.MonitorAt(nil, 0, 0); ok {
		t.Error("sin pantallas no puede devolver una")
	}
}

func TestPrimaryYByNameEligenLaQueCorresponde(t *testing.T) {
	m, ok := x11.PrimaryMonitor(todas())
	if !ok || m.Name != "HDMI-1-0" {
		t.Errorf("la principal es HDMI-1-0, dio %q", m.Name)
	}
	// Sin ninguna marcada, la primera sirve: mejor que no mostrar nada.
	sin := []x11.Monitor{{Name: "a"}, {Name: "b"}}
	if m, _ := x11.PrimaryMonitor(sin); m.Name != "a" {
		t.Errorf("dio %q", m.Name)
	}
	if m, ok := x11.MonitorByName(todas(), "DP-1-2"); !ok || m.Width != 1680 {
		t.Errorf("no encontró DP-1-2: %+v", m)
	}
	if _, ok := x11.MonitorByName(todas(), "VGA-9"); ok {
		t.Error("una pantalla que no está no puede aparecer")
	}
}

func TestContainsMiraLosBordes(t *testing.T) {
	if !tele.Contains(0, 0) {
		t.Error("la esquina de arriba a la izquierda está adentro")
	}
	if tele.Contains(1920, 0) {
		t.Error("el primer píxel de la pantalla de al lado ya no está adentro")
	}
	if tele.Contains(0, 1080) {
		t.Error("la fila de abajo del rectángulo ya no está adentro")
	}
}
