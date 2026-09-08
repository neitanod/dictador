package x11

import "testing"

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

// idioma es lo que quedó elegido, o "" si no hay ninguno.
func idioma(t *langTracker) string {
	key, ok := t.current()
	if !ok {
		return ""
	}
	return key.Language
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

// Un toque prende el idioma, y soltar la tecla no lo apaga: es un interruptor,
// no un botón que hay que tener hundido.
func TestUnToqueEnciendeElIdioma(t *testing.T) {
	tracker := trackerConTeclas(t)

	tracker.press(26, false)
	if got := idioma(tracker); got != "en" {
		t.Fatalf("quería inglés, dio %q", got)
	}
}

// El segundo toque de la misma letra lo apaga.
func TestElSegundoToqueApaga(t *testing.T) {
	tracker := trackerConTeclas(t)

	tracker.press(26, false)
	tracker.press(26, false)

	if got := idioma(tracker); got != "" {
		t.Fatalf("quería nada, dio %q", got)
	}
}

// Otra letra pisa a la anterior: estando en inglés, tocar la "p" deja
// portugués, sin pasar por apagado.
func TestOtraLetraPisaALaAnterior(t *testing.T) {
	tracker := trackerConTeclas(t)

	tracker.press(26, false)
	tracker.press(33, false)

	if got := idioma(tracker); got != "pt" {
		t.Fatalf("quería portugués, dio %q", got)
	}
	// Y la que ahora apaga es la que está prendida, no la de antes.
	tracker.press(26, false)
	if got := idioma(tracker); got != "en" {
		t.Fatalf("quería inglés, dio %q", got)
	}
}

// Dejar la tecla hundida hace que X la repita sola. Esos repetidos no son
// toques: si contaran, el idioma prendería y apagaría treinta veces por
// segundo y quedaría en cualquiera.
func TestElAutorrepetidoNoCuentaComoToque(t *testing.T) {
	tracker := trackerConTeclas(t)

	tracker.press(26, false)
	for i := 0; i < 20; i++ {
		tracker.press(26, true) // X repitiendo mientras el dedo sigue abajo
	}

	if got := idioma(tracker); got != "en" {
		t.Fatalf("quería inglés, dio %q", got)
	}
}

func TestSinTocarNadaNoSeTraduce(t *testing.T) {
	if got := idioma(trackerConTeclas(t)); got != "" {
		t.Fatalf("quería nada, dio %q", got)
	}
}

// Lo que elegiste en el dictado anterior no puede colarse en el que sigue, si
// el idioma pegajoso está apagado: pegar traducido sin querer se descubre
// después de pegarlo.
func TestCadaDictadoArrancaSinIdioma(t *testing.T) {
	tracker := trackerConTeclas(t)
	tracker.press(26, false)

	tracker.reset(LanguageKey{})

	if got := idioma(tracker); got != "" {
		t.Fatalf("quería nada, dio %q", got)
	}
	tracker.press(26, false)
	if got := idioma(tracker); got != "en" {
		t.Fatalf("quería inglés, dio %q", got)
	}
}

// Con el idioma pegajoso, el dictado arranca donde quedó el anterior, y la
// letra sigue funcionando igual: un toque lo apaga.
func TestConElIdiomaPegajosoElDictadoArrancaDondeQuedó(t *testing.T) {
	tracker := trackerConTeclas(t)
	tracker.press(33, false) // portugués

	elegido, _ := tracker.current()
	tracker.reset(elegido) // el dictado siguiente, con el pegajoso prendido

	if got := idioma(tracker); got != "pt" {
		t.Fatalf("quería portugués, dio %q", got)
	}
	tracker.press(33, false)
	if got := idioma(tracker); got != "" {
		t.Fatalf("el toque tendría que haberlo apagado, dio %q", got)
	}
}

// Una tecla que no es de idioma no cambia nada, aunque llegue al tracker.
func TestUnaTeclaAjenaNoCambiaElIdioma(t *testing.T) {
	tracker := trackerConTeclas(t)
	tracker.press(26, false)

	tracker.press(105, false) // Control_R, que no es de idioma

	if got := idioma(tracker); got != "en" {
		t.Fatalf("quería inglés, dio %q", got)
	}
}
