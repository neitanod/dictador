package commands

import "testing"

// plain compila con los comandos prendidos y devuelve el texto plano, que es
// lo que se ve en el 90% de los casos.
func plain(t *testing.T, said string) string {
	t.Helper()
	return Compile(said, Options{Enabled: true}).Text()
}

func TestSinComandosElTextoSaleIgualPeroConLosEspaciosNormalizados(t *testing.T) {
	got := Compile("  hola   mundo \n del dictador ", Options{Enabled: true}).Text()
	if got != "hola mundo del dictador" {
		t.Errorf("quedó %q", got)
	}
}

func TestApagadoNoReemplazaNada(t *testing.T) {
	got := Compile("hola coma qué tal", Options{}).Text()
	if got != "hola coma qué tal" {
		t.Errorf("quedó %q", got)
	}
}

func TestPuntuaciónSePegaALaPalabraAnterior(t *testing.T) {
	cases := map[string]string{
		"hola coma qué tal":                   "hola, qué tal",
		"listo punto y coma seguimos":         "listo; seguimos",
		"vino coma vio coma venció":           "vino, vio, venció",
		"terminá eso punto y seguido gracias": "terminá eso. gracias",
	}
	for said, want := range cases {
		if got := plain(t, said); got != want {
			t.Errorf("%q → %q, quería %q", said, got, want)
		}
	}
}

func TestLosSignosQueAbrenSePeganALoQueSigue(t *testing.T) {
	cases := map[string]string{
		"abre pregunta cómo andás signo de pregunta":    "¿cómo andás?",
		"abre admiración qué bueno signo de admiración": "¡qué bueno!",
		"abre interrogación listo cierra interrogación": "¿listo?",
	}
	for said, want := range cases {
		if got := plain(t, said); got != want {
			t.Errorf("%q → %q, quería %q", said, got, want)
		}
	}
}

func TestLosSímbolosPegadosArmanRutasYPalabrasCompuestas(t *testing.T) {
	cases := map[string]string{
		"carpeta barra archivo":       "carpeta/archivo",
		"ce barra invertida ene":      "ce\\ene",
		"nombre guión bajo largo":     "nombre_largo",
		"teórico guión práctico":      "teórico-práctico",
		"asterisco negrita asterisco": "*negrita*",
		"signo pesos 100":             "$100",
		"numeral dictador":            "#dictador",
		"hashtag dictador":            "#dictador",
	}
	for said, want := range cases {
		if got := plain(t, said); got != want {
			t.Errorf("%q → %q, quería %q", said, got, want)
		}
	}
}

func TestLosOperadoresVanSueltos(t *testing.T) {
	cases := map[string]string{
		"dos signo más dos signo igual cuatro": "dos + dos = cuatro",
		"a ampersand b":                        "a & b",
		"x signo mayor y":                      "x > y",
		"x signo menor y":                      "x < y",
	}
	for said, want := range cases {
		if got := plain(t, said); got != want {
			t.Errorf("%q → %q, quería %q", said, got, want)
		}
	}
}

func TestEntreCorchetesDejaElCursorAdentro(t *testing.T) {
	plan := Compile("entre corchetes", Options{Enabled: true})
	want := []Step{{Text: "[]"}, {Key: "Left"}}
	assertSteps(t, plan, want)

	// Lo que sigue dictado entra adentro, sin espacio de por medio.
	if got := plain(t, "entre corchetes hola"); got != "[hola]" {
		t.Errorf("quedó %q", got)
	}
	if got := plain(t, "entre paréntesis nota"); got != "(nota)" {
		t.Errorf("quedó %q", got)
	}
	if got := plain(t, "entre comillas cita"); got != "\"cita\"" {
		t.Errorf("quedó %q", got)
	}
}

func TestEnterYTabSonTeclasDeVerdad(t *testing.T) {
	assertSteps(t, Compile("hola enter chau", Options{Enabled: true}), []Step{
		{Text: "hola"}, {Key: "Return"}, {Text: "chau"},
	})
	assertSteps(t, Compile("nombre tab apellido", Options{Enabled: true}), []Step{
		{Text: "nombre"}, {Key: "Tab"}, {Text: "apellido"},
	})
	// Y en texto plano —lo que va al clipboard— son el carácter que les toca.
	if got := plain(t, "hola enter chau"); got != "hola\nchau" {
		t.Errorf("quedó %q", got)
	}
	if got := plain(t, "nombre tab apellido"); got != "nombre\tapellido" {
		t.Errorf("quedó %q", got)
	}
}

func TestPuntoYAparteEsPuntoMásSaltoDeLínea(t *testing.T) {
	assertSteps(t, Compile("listo punto y aparte seguimos", Options{Enabled: true}), []Step{
		{Text: "listo."}, {Key: "Return"}, {Text: "seguimos"},
	})
}

func TestElPuntoNoSeRepiteSobreOtroSigno(t *testing.T) {
	// El signo que cierra ya puso el punto: "punto y aparte" sólo tiene que
	// bajar de renglón.
	assertSteps(t, Compile("listo signo de pregunta punto y aparte seguimos", Options{Enabled: true}), []Step{
		{Text: "listo?"}, {Key: "Return"}, {Text: "seguimos"},
	})
	if got := plain(t, "genial signo de admiración punto y seguido dale"); got != "genial! dale" {
		t.Errorf("quedó %q", got)
	}
	// Y el motor de voz que ya puntuó tampoco tiene que terminar en "..".
	if got := plain(t, "listo. punto y seguido dale"); got != "listo. dale" {
		t.Errorf("quedó %q", got)
	}
}

func TestEspacioEspacioSonDosEspacios(t *testing.T) {
	if got := plain(t, "final espacio espacio"); got != "final  " {
		t.Errorf("quedó %q", got)
	}
}

// Los comandos son frases que uno dice cuando habla de editar texto. Las
// palabras que aparecen en cualquier charla no pueden ser comandos, aunque
// nombren al signo: "el alfajor Capitán del Espacio es muy rico".
func TestLasPalabrasComunesNoSonComandos(t *testing.T) {
	cases := map[string]string{
		"el alfajor Capitán del Espacio es muy rico": "el alfajor Capitán del Espacio es muy rico",
		"un número mayor que el otro":                "un número mayor que el otro",
		"dejá un espacio en blanco":                  "dejá un espacio en blanco",
		"le puso punto final al asunto":              "le puso punto final al asunto",
		"borralo cuando puedas":                      "borralo cuando puedas",
	}
	for said, want := range cases {
		if got := plain(t, said); got != want {
			t.Errorf("%q → %q, quería %q", said, got, want)
		}
	}
}

func TestBorrarPalabraSacaLaÚltimaDictada(t *testing.T) {
	if got := plain(t, "hola mundo borrar palabra chau"); got != "hola chau" {
		t.Errorf("quedó %q", got)
	}
	// "borrá" y "borrar" son la misma orden, y si la palabra borrada era la
	// única el dictado se queda sin nada para escribir.
	if got := plain(t, "hola mundo borrá palabra"); got != "hola" {
		t.Errorf("quedó %q", got)
	}
	if got := plain(t, "hola borrá palabra"); got != "" {
		t.Errorf("quedó %q", got)
	}
	// Sin nada dictado todavía, le pide al editor que borre la palabra de atrás.
	assertSteps(t, Compile("borrar palabra", Options{Enabled: true}), []Step{
		{Key: "ctrl+BackSpace"},
	})
}

func TestBorráEsoTiraTodoLoDictadoHastaAhí(t *testing.T) {
	if got := plain(t, "esto está mal borrá eso esto está bien"); got != "esto está bien" {
		t.Errorf("quedó %q", got)
	}
	// Incluso si en el medio hubo teclas: nada de eso se ejecutó todavía.
	assertSteps(t, Compile("uno enter dos borrá eso tres", Options{Enabled: true}), []Step{
		{Text: "tres"},
	})
	// Dicho al principio no hace nada: el texto que ya estaba en tu editor no
	// lo escribió el dictado, y no es nuestro para borrarlo.
	assertSteps(t, Compile("borrá eso", Options{Enabled: true}), nil)
}

func TestElMatchIgnoraTildesMayúsculasYLaPuntuaciónQueMeteElMotor(t *testing.T) {
	cases := map[string]string{
		"Hola, Coma. qué tal":         "Hola,, qué tal",
		"hola guion bajo mundo":       "hola_mundo",
		"carpeta Barra archivo":       "carpeta/archivo",
		"esto está mal, borra eso ok": "ok",
	}
	for said, want := range cases {
		if got := plain(t, said); got != want {
			t.Errorf("%q → %q, quería %q", said, got, want)
		}
	}
}

func TestGanaLaFraseMásLarga(t *testing.T) {
	// "punto y coma" no puede caer en "punto y aparte", ni "guión bajo" en "guión".
	if got := plain(t, "a punto y coma b"); got != "a; b" {
		t.Errorf("quedó %q", got)
	}
	if got := plain(t, "a guión bajo b"); got != "a_b" {
		t.Errorf("quedó %q", got)
	}
	if got := plain(t, "a barra invertida b"); got != "a\\b" {
		t.Errorf("quedó %q", got)
	}
}

func TestReemplazosDelUsuario(t *testing.T) {
	opts := Options{Enabled: true, Replacements: map[string]string{
		"flecha":     "→",
		"dos puntos": ":",
		"asterisco":  "", // vaciarlo apaga el built-in
	}}
	if got := Compile("acá flecha allá", opts).Text(); got != "acá → allá" {
		t.Errorf("quedó %q", got)
	}
	if got := Compile("nota dos puntos esto", opts).Text(); got != "nota: esto" {
		t.Errorf("quedó %q", got)
	}
	if got := Compile("un asterisco suelto", opts).Text(); got != "un asterisco suelto" {
		t.Errorf("quedó %q", got)
	}
}

func TestElUsuarioPuedeCambiarUnBuiltIn(t *testing.T) {
	opts := Options{Enabled: true, Replacements: map[string]string{"coma": " —"}}
	if got := Compile("hola coma qué tal", opts).Text(); got != "hola — qué tal" {
		t.Errorf("quedó %q", got)
	}
}

func TestPlanVacíoYTeclas(t *testing.T) {
	if !Compile("   ", Options{Enabled: true}).Empty() {
		t.Error("un dictado en blanco tendría que dar un plan vacío")
	}
	if Compile("hola mundo", Options{Enabled: true}).HasKeys() {
		t.Error("un dictado sin comandos no necesita teclas")
	}
	if !Compile("hola enter", Options{Enabled: true}).HasKeys() {
		t.Error("un enter es una tecla")
	}
}

func TestRetoquesFinales(t *testing.T) {
	plan := Compile("hola mundo.", Options{Enabled: true})
	plan.StripFinalPeriod()
	plan.AppendText(" ")
	if got := plan.Text(); got != "hola mundo " {
		t.Errorf("quedó %q", got)
	}

	// El punto se saca sólo si el dictado termina en texto: si termina en una
	// tecla, no hay nada que recortar.
	conTecla := Compile("hola mundo. enter", Options{Enabled: true})
	conTecla.StripFinalPeriod()
	if got := conTecla.Text(); got != "hola mundo.\n" {
		t.Errorf("quedó %q", got)
	}

	// Y sólo el último punto, como hacía el postproceso viejo.
	tres := Compile("hola mundo...", Options{Enabled: true})
	tres.StripFinalPeriod()
	if got := tres.Text(); got != "hola mundo.." {
		t.Errorf("quedó %q", got)
	}
}

func TestTodosLosComandosPedidosTienenRegla(t *testing.T) {
	pedidos := []string{
		"punto y aparte", "coma", "punto y coma", "enter", "tab", "signo pesos",
		"guión", "guión bajo", "barra", "barra invertida", "signo de interrogación",
		"abre pregunta", "signo de pregunta", "abre admiración", "signo de admiración",
		"entre corchetes", "entre comillas", "entre paréntesis", "signo más",
		"signo igual", "asterisco", "ampersand", "numeral", "hashtag", "signo mayor",
		"signo menor", "espacio espacio", "borrar palabra", "borrá eso",
	}
	for _, said := range pedidos {
		if _, ok := builtin[normalize(said)]; !ok {
			t.Errorf("falta la regla de %q", said)
		}
	}
}

func assertSteps(t *testing.T, plan Plan, want []Step) {
	t.Helper()
	if len(plan.Steps) != len(want) {
		t.Fatalf("%d pasos (%v), quería %d (%v)", len(plan.Steps), plan.Steps, len(want), want)
	}
	for i := range want {
		if plan.Steps[i] != want[i] {
			t.Errorf("paso %d: %v, quería %v", i, plan.Steps[i], want[i])
		}
	}
}
