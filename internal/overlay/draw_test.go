package overlay

import (
	"testing"

	"golang.org/x/image/font"
)

func TestNewMetricsEscalaTodoJunto(t *testing.T) {
	base := newMetrics(96)
	if base.pad != 18 || base.minHeight != 96 {
		t.Errorf("a 96 DPI la geometría es la escrita: %+v", base)
	}
	// El doble de densidad es el doble de todo: si sólo creciera la letra, el
	// texto se comería el margen.
	doble := newMetrics(192)
	if doble.pad != 2*base.pad || doble.minHeight != 2*base.minHeight {
		t.Errorf("no escaló parejo: %+v", doble)
	}
	if doble.radius != 2*base.radius || doble.barHeight != 2*base.barHeight {
		t.Errorf("no escaló parejo: %+v", doble)
	}
	// Un DPI inválido no puede dar una ventanita de tamaño cero.
	if cero := newMetrics(0); cero.pad != base.pad {
		t.Errorf("con DPI 0 tendría que caer a 96: %+v", cero)
	}
}

// TestLaLetraCreceConElDPI es el test del bug: rasterizar a 72 DPI toma un punto
// por un píxel y deja la letra un 25% más chica que la de cualquier toolkit, que
// dibuja a los 96 DPI que declara la pantalla.
func TestLaLetraCreceConElDPI(t *testing.T) {
	if FontFile() == "" {
		t.Skip("no hay ninguna fuente TrueType en esta máquina")
	}
	chica, err := loadFaces(19, 72)
	if err != nil {
		t.Fatal(err)
	}
	defer chica.close()
	normal, err := loadFaces(19, 96)
	if err != nil {
		t.Fatal(err)
	}
	defer normal.close()

	const muestra = "esto es lo que se va entendiendo mientras hablás"
	a := font.MeasureString(chica.body, muestra).Ceil()
	b := font.MeasureString(normal.body, muestra).Ceil()
	if b <= a {
		t.Fatalf("a 96 DPI tendría que medir más: %d px contra %d px", b, a)
	}
	// 96/72 es un tercio más grande; se le deja aire por el hinting.
	ratio := float64(b) / float64(a)
	if ratio < 1.25 || ratio > 1.42 {
		t.Errorf("la relación entre 96 y 72 DPI dio %.2f, quería ~1.33", ratio)
	}
}

func TestWrapCortaPorPalabra(t *testing.T) {
	if FontFile() == "" {
		t.Skip("no hay ninguna fuente TrueType en esta máquina")
	}
	fc, err := loadFaces(19, 96)
	if err != nil {
		t.Fatal(err)
	}
	defer fc.close()

	lines := wrap("una frase larga que no entra de una sola vez en el ancho", fc.body, 200)
	if len(lines) < 2 {
		t.Fatalf("tendría que cortar en varias líneas: %v", lines)
	}
	for _, line := range lines {
		if measure(fc.body, line) > 200 {
			t.Errorf("la línea %q no entra en 200 px", line)
		}
	}
	// Sin texto no hay líneas raras: una vacía y nada más.
	if got := wrap("   ", fc.body, 200); len(got) != 1 || got[0] != "" {
		t.Errorf("con texto vacío dio %q", got)
	}
}

func TestFormatSecondsRedondea(t *testing.T) {
	for _, c := range []struct {
		in   float64
		want string
	}{{0, "0.0s"}, {5.1, "5.1s"}, {5.14, "5.1s"}, {5.16, "5.2s"}, {12, "12.0s"}} {
		if got := formatSeconds(c.in); got != c.want {
			t.Errorf("%v → %q, quería %q", c.in, got, c.want)
		}
	}
}
