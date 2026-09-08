package x11

import (
	"testing"
	"time"
)

// fakeLangKeymap es un teclado con las dos letras que vienen de fábrica.
func fakeLangKeymap() *Keymap {
	rows := []struct {
		code int
		syms []string
	}{
		{26, []string{"e", "E"}},
		{33, []string{"p", "P"}},
		{105, []string{"Control_R", "Control_R"}},
	}
	const per = 2
	min, max := rows[0].code, rows[len(rows)-1].code
	syms := make([]uint32, (max-min+1)*per)
	for _, r := range rows {
		for i, name := range r.syms {
			sym, ok := keysymByName[name]
			if !ok {
				panic("keysym que no existe en la tabla: " + name)
			}
			syms[(r.code-min)*per+i] = sym
		}
	}
	return &Keymap{MinKeycode: min, PerKeycode: per, Keysyms: syms}
}

func trackerConTeclas(t *testing.T) *langTracker {
	t.Helper()
	keys, problems := NewLanguageKeys(map[string]string{"e": "en", "p": "pt"}, fakeLangKeymap())
	if len(problems) > 0 {
		t.Fatalf("no pude resolver las teclas: %v", problems)
	}
	tracker := newLangTracker()
	tracker.keys = keys
	return tracker
}

func TestLaLetraQueNoEstaEnElTecladoAvisaYNoTumbaAlResto(t *testing.T) {
	keys, problems := NewLanguageKeys(map[string]string{"e": "en", "ß": "de"}, fakeLangKeymap())
	if len(problems) != 1 {
		t.Fatalf("quería un problema por la letra que falta, hubo %d: %v", len(problems), problems)
	}
	if _, ok := keys.Lookup(26); !ok {
		t.Error("la letra buena tendría que haber quedado igual")
	}
}

func TestConLaLetraApretadaAlSoltarSeEligeEseIdioma(t *testing.T) {
	tracker := trackerConTeclas(t)
	tracker.press(26)

	key, ok := tracker.current(map[int]bool{26: true})
	if !ok || key.Language != "en" {
		t.Fatalf("quería inglés, dio %+v (ok=%v)", key, ok)
	}
}

// Probar con una y terminar en otra tiene que quedarse con la última: es lo que
// hace la mano cuando se arrepiente a mitad de la frase.
func TestGanaLaUltimaLetraApretada(t *testing.T) {
	tracker := trackerConTeclas(t)
	tracker.press(26)
	tracker.press(33)

	key, ok := tracker.current(map[int]bool{26: true, 33: true})
	if !ok || key.Language != "pt" {
		t.Fatalf("quería portugués, dio %+v (ok=%v)", key, ok)
	}
}

// Soltar el combo y la letra es un solo movimiento, y los dedos no se levantan
// sincronizados: la letra que se acaba de soltar todavía cuenta.
func TestLaLetraSoltadaUnInstanteAntesTodaviaCuenta(t *testing.T) {
	tracker := trackerConTeclas(t)
	tracker.press(26)
	tracker.release(26)

	key, ok := tracker.current(nil)
	if !ok || key.Language != "en" {
		t.Fatalf("quería inglés, dio %+v (ok=%v)", key, ok)
	}
}

func TestLaLetraSoltadaHaceRatoYaNoCuenta(t *testing.T) {
	tracker := trackerConTeclas(t)
	tracker.press(26)
	tracker.release(26)
	tracker.released[26] = time.Now().Add(-2 * languageGrace)

	if key, ok := tracker.current(nil); ok {
		t.Fatalf("no tendría que haber idioma, dio %+v", key)
	}
}

// El press que no vimos —llegó mientras otra app tenía el teclado agarrado— se
// recupera preguntándole al servidor X qué sigue hundido.
func TestLaTeclaQueSoloVeXTambienElige(t *testing.T) {
	tracker := trackerConTeclas(t)

	key, ok := tracker.current(map[int]bool{33: true})
	if !ok || key.Language != "pt" {
		t.Fatalf("quería portugués, dio %+v (ok=%v)", key, ok)
	}
}

func TestSinNingunaLetraNoSeTraduce(t *testing.T) {
	tracker := trackerConTeclas(t)
	if key, ok := tracker.current(nil); ok {
		t.Fatalf("no tendría que haber idioma, dio %+v", key)
	}
}

// Lo que apretaste en el dictado anterior no puede elegir el idioma del que
// sigue.
func TestCadaDictadoArrancaSinIdioma(t *testing.T) {
	tracker := trackerConTeclas(t)
	tracker.press(26)
	tracker.reset()

	if key, ok := tracker.current(nil); ok {
		t.Fatalf("no tendría que haber idioma, dio %+v", key)
	}
}

// El texto en vivo de la ventanita sigue al dedo sin esperar la gracia: soltar
// la letra mientras hablás quiere decir que ya no querés traducir.
func TestElCartelEnVivoSigueAlDedo(t *testing.T) {
	tracker := trackerConTeclas(t)
	tracker.press(26)
	if got := tracker.live(); got.Language != "en" {
		t.Fatalf("quería inglés, dio %+v", got)
	}
	tracker.release(26)
	if got := tracker.live(); got.Language != "" {
		t.Fatalf("quería nada, dio %+v", got)
	}
}
