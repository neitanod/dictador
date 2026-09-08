package translate

import "testing"

func TestNombreDeUnIdioma(t *testing.T) {
	casos := []struct{ code, want string }{
		{"en", "Inglés"},
		{"EN", "Inglés"},
		{"zh-CN", "Chino (simplificado)"},
		// Una variante que no está en el catálogo se lee por su idioma base:
		// "en-GB" es inglés aunque nadie lo haya escrito en la lista.
		{"en-GB", "Inglés"},
		// Y un código que no conocemos se muestra tal cual, que es mejor que
		// inventarle un nombre: lo escribió alguien a mano en el config.
		{"tlh", "tlh"},
		{"", ""},
	}
	for _, c := range casos {
		if got := Name(c.code); got != c.want {
			t.Errorf("Name(%q) = %q, quería %q", c.code, got, c.want)
		}
	}
}

func TestLaLetraSeGuardaComoUnaSolaMinuscula(t *testing.T) {
	casos := []struct{ in, want string }{
		{"e", "e"},
		{"E", "e"},
		{" p ", "p"},
		{"ñ", "ñ"},
		{"", ""},
		{"en", ""},
		{" ", ""},
	}
	for _, c := range casos {
		if got := NormalizeKey(c.in); got != c.want {
			t.Errorf("NormalizeKey(%q) = %q, quería %q", c.in, got, c.want)
		}
	}
}

func TestElCodigoDeIdiomaSeEscribeComoLoEsperaGoogle(t *testing.T) {
	casos := []struct{ in, want string }{
		{"EN", "en"},
		{"zh-cn", "zh-CN"},
		{" pt ", "pt"},
	}
	for _, c := range casos {
		if got := NormalizeCode(c.in); got != c.want {
			t.Errorf("NormalizeCode(%q) = %q, quería %q", c.in, got, c.want)
		}
	}
}

// La tabla se muestra siempre en el mismo orden y sin las filas a medio llenar:
// una fila sin letra o sin idioma es la que quedó abierta en la pantalla de
// configuración, y guardarla dejaría una entrada muerta en el archivo.
func TestLaTablaSeLimpiaYSeOrdena(t *testing.T) {
	got := Bindings(map[string]string{
		"P":  "PT",
		"e":  "en",
		"":   "fr",
		"x":  "",
		"  ": "it",
	})
	want := []Binding{{Key: "e", Language: "en"}, {Key: "p", Language: "pt"}}
	if len(got) != len(want) {
		t.Fatalf("quedaron %d entradas (%v), quería %d", len(got), got, len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("entrada %d = %v, quería %v", i, got[i], want[i])
		}
	}
}
