package x11

import (
	"strings"
	"testing"
)

// fakeKeymap arma un mapa de teclado como el que devolvería el servidor, para
// poder probar la resolución de combos sin un display.
func fakeKeymap() *Keymap {
	// Un teclado de mentira con cuatro teclas, dos keysyms por keycode.
	rows := []struct {
		code int
		syms []string
	}{
		{37, []string{"Control_L", "Control_L"}},
		{105, []string{"Control_R", "Control_R"}},
		{108, []string{"ISO_Level3_Shift", "ISO_Level3_Shift"}},
		{55, []string{"v", "V"}},
	}
	min := rows[0].code
	// El mapa es ddenso: hay que rellenar los keycodes que no existen.
	max := rows[len(rows)-1].code
	for _, r := range rows {
		if r.code > max {
			max = r.code
		}
	}
	const per = 2
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

func TestParseComboResuelveElGatilloYLosModificadores(t *testing.T) {
	combo, err := ParseCombo("AltGr+Control_R", fakeKeymap())
	if err != nil {
		t.Fatal(err)
	}
	if combo.TriggerName != "Control_R" {
		t.Errorf("el gatillo es lo último: %q", combo.TriggerName)
	}
	if !combo.Trigger[105] {
		t.Errorf("el gatillo tendría que ser el keycode 105: %v", combo.Trigger)
	}
	if len(combo.Mods) != 1 || !combo.Mods[0][108] {
		t.Errorf("AltGr tendría que resolver a ISO_Level3_Shift (108): %v", combo.Mods)
	}
	if !combo.All[105] || !combo.All[108] {
		t.Errorf("All tiene que tener las dos: %v", combo.All)
	}
}

func TestParseComboAceptaUnaTeclaSola(t *testing.T) {
	combo, err := ParseCombo("Control_R", fakeKeymap())
	if err != nil {
		t.Fatal(err)
	}
	if len(combo.Mods) != 0 {
		t.Errorf("sin '+' no hay modificadores: %v", combo.Mods)
	}
	if !combo.held(nil) {
		t.Error("sin modificadores, siempre están 'apretados'")
	}
}

func TestParseComboAceptaLosAliasGenericos(t *testing.T) {
	// `ctrl` vale para cualquiera de los dos lados del teclado.
	combo, err := ParseCombo("ctrl", fakeKeymap())
	if err != nil {
		t.Fatal(err)
	}
	if !combo.Trigger[37] || !combo.Trigger[105] {
		t.Errorf("ctrl tendría que ser los dos Control: %v", combo.Trigger)
	}
}

func TestParseComboSeQuejaDeLoQueNoExiste(t *testing.T) {
	if _, err := ParseCombo("", fakeKeymap()); err == nil {
		t.Error("una tecla vacía tiene que fallar")
	}
	_, err := ParseCombo("Tecla_Inventada", fakeKeymap())
	if err == nil || !strings.Contains(err.Error(), "Tecla_Inventada") {
		t.Errorf("tendría que nombrar la tecla que no existe: %v", err)
	}
	// Un keysym real que este teclado no tiene es otro error, y también avisa.
	_, err = ParseCombo("Menu", fakeKeymap())
	if err == nil || !strings.Contains(err.Error(), "mapa de teclado") {
		t.Errorf("tendría que decir que no está en este teclado: %v", err)
	}
}

func TestComboHeldMiraTodosLosModificadores(t *testing.T) {
	combo, err := ParseCombo("AltGr+ctrl+Control_R", fakeKeymap())
	if err != nil {
		t.Fatal(err)
	}
	if combo.held(map[int]bool{108: true}) {
		t.Error("falta el Control: no tendría que dar por completo el combo")
	}
	if !combo.held(map[int]bool{108: true, 37: true}) {
		t.Error("con AltGr y un Control alcanza")
	}
}

func TestDescribeCuentaLoQueQuedoEscuchando(t *testing.T) {
	combo, _ := ParseCombo("AltGr+Control_R", fakeKeymap())
	got := combo.Describe()
	if got != "AltGr + Control_R (keycode 105)" {
		t.Errorf("Describe = %q", got)
	}
	solo, _ := ParseCombo("Control_L", fakeKeymap())
	if solo.Describe() != "Control_L (keycode 37)" {
		t.Errorf("Describe = %q", solo.Describe())
	}
}

func TestKeysListaElMapaConNombres(t *testing.T) {
	keys := fakeKeymap().Keys()
	found := map[int]string{}
	for _, k := range keys {
		found[k.Keycode] = k.Name
	}
	if found[108] != "ISO_Level3_Shift" {
		t.Errorf("el keycode 108 se llama %q", found[108])
	}
	if found[55] != "v" {
		t.Errorf("el keycode 55 se llama %q", found[55])
	}
	if _, ok := found[38]; ok {
		t.Error("los keycodes sin keysym no van en la lista")
	}
}

func TestKeysymNameConoceLasTeclasQueImportan(t *testing.T) {
	// AltGr es la que python-xlib no encontraba sin cargar el grupo "xkb".
	sym, ok := Keysym("ISO_Level3_Shift")
	if !ok {
		t.Fatal("ISO_Level3_Shift tiene que estar en la tabla")
	}
	if got := KeysymName(sym); got != "ISO_Level3_Shift" {
		t.Errorf("la vuelta da %q", got)
	}
	// Y los keysym Unicode se resuelven al carácter.
	if got := KeysymName(0x01000101); got != "ā" {
		t.Errorf("keysym Unicode → %q", got)
	}
}

func TestIsTerminalReconoceLasQuePeganConCtrlShiftV(t *testing.T) {
	for _, class := range []string{"konsole", "Konsole", "alacritty", "ghostty"} {
		if !IsTerminal(class) {
			t.Errorf("%q es una terminal", class)
		}
	}
	for _, class := range []string{"firefox", "code", ""} {
		if IsTerminal(class) {
			t.Errorf("%q no es una terminal", class)
		}
	}
}

func TestRuneKeysymSigueLaConvencionDeX(t *testing.T) {
	if got := runeKeysym('a'); got != 0x61 {
		t.Errorf("Latin-1 va directo: %#x", got)
	}
	if got := runeKeysym('ñ'); got != 0xf1 {
		t.Errorf("ñ está en Latin-1: %#x", got)
	}
	if got := runeKeysym('…'); got != 0x01002026 {
		t.Errorf("fuera de Latin-1 va con el prefijo Unicode: %#x", got)
	}
}

func TestLookupEncuentraLaTeclaYSiVaConShift(t *testing.T) {
	k := fakeKeymap()
	code, shift, ok := k.lookup('v')
	if !ok || code != 55 || shift {
		t.Errorf("v = (%d, %v, %v)", code, shift, ok)
	}
	code, shift, ok = k.lookup('V')
	if !ok || code != 55 || !shift {
		t.Errorf("V = (%d, %v, %v)", code, shift, ok)
	}
	if _, _, ok := k.lookup('ñ'); ok {
		t.Error("una tecla que este teclado no tiene se resuelve prestando un keycode")
	}
}

func TestSpareKeycodeEncuentraUnoLibre(t *testing.T) {
	// En el teclado de mentira, los keycodes entre 37 y 105 están vacíos.
	if got := fakeKeymap().spareKeycode(); got != 38 {
		t.Errorf("el primer keycode libre es el 38, dio %d", got)
	}
}

func TestParseDisplayPartElNombre(t *testing.T) {
	cases := []struct {
		spec   string
		host   string
		num    int
		screen int
	}{
		{":0", "", 0, 0},
		{":99", "", 99, 0},
		{":0.1", "", 0, 1},
		{"maquina:12.0", "maquina", 12, 0},
	}
	for _, c := range cases {
		host, num, screen, err := parseDisplay(c.spec)
		if err != nil {
			t.Errorf("%q: %v", c.spec, err)
			continue
		}
		if host != c.host || num != c.num || screen != c.screen {
			t.Errorf("%q → (%q, %d, %d)", c.spec, host, num, screen)
		}
	}
	if _, _, _, err := parseDisplay("no-es-un-display"); err == nil {
		t.Error("un DISPLAY inválido tiene que fallar")
	}
}
