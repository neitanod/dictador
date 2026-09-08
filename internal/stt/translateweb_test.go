package stt

import (
	"strings"
	"testing"
	"time"

	"github.com/neitanod/dictador/internal/config"
)

func chromeParaTraducir(t *testing.T) *Chrome {
	t.Helper()
	if testing.Short() {
		t.Skip("lanza un Chrome de verdad y sale a internet")
	}
	if ChromeBinary("") == "" {
		t.Skip("no hay Chrome en esta máquina")
	}
	chrome := NewChrome(Options{
		STT: config.STT{ChromeHeadless: true, ChromeReadyTimeoutS: 30},
		Translate: config.Translate{
			Enabled: true, Mode: "web", TimeoutS: 20,
			Keys: map[string]string{"e": "en"},
		},
	})
	t.Cleanup(chrome.Close)
	if err := chrome.Load(); err != nil {
		t.Skipf("este Chrome no levantó la página de dictado: %v", err)
	}
	return chrome
}

// El hallazgo que costó la noche del 08/09/2026, convertido en test.
//
// Para manejar la página del traductor hay que abrirle a Chrome un puerto de
// control. Pidiéndole a Chrome que lo elija él —el clásico `--remote-debugging-port=0`,
// que es lo que hace cualquiera— Chrome prende adentro la bandera que dice que
// lo está manejando un programa. Google la lee y sirve el traductor viejo, el
// que traduce palabra por palabra. Con el puerto elegido de antemano, la misma
// página traduce por sentido.
//
// Sin este test, alguien vuelve a poner el 0 —porque es lo obvio— y la
// traducción empeora sin que nada falle ni avise.
func TestLaPaginaNoSabeQueLaManejamos(t *testing.T) {
	chrome := chromeParaTraducir(t)

	web, err := chrome.translator()
	if err != nil {
		t.Fatalf("no pude armar el traductor: %v", err)
	}
	tab, err := web.Tab("en")
	if err != nil {
		t.Fatalf("no pude abrir la página del traductor: %v", err)
	}
	raw, err := tab.conn.Evaluate("navigator.webdriver === true", 10*time.Second)
	if err != nil {
		t.Fatalf("no pude preguntarle a la página: %v", err)
	}
	if string(raw) == "true" {
		t.Error("la página nos ve como un programa: Google va a servir el traductor viejo")
	}
}

// Y el resultado de todo eso, que es lo único que se ve desde afuera: la
// traducción tiene que ser la que traduce por sentido.
func TestLaTraduccionEsLaDeLaWebYNoLaLiteral(t *testing.T) {
	chrome := chromeParaTraducir(t)

	got, err := chrome.Translate("Mañana voy a estar hecho mierda", "en")
	if err != nil {
		t.Fatalf("no tradujo: %v", err)
	}
	if !got.Web {
		t.Fatalf("cayó al endpoint: %q", got.Text)
	}
	// El endpoint devuelve "Tomorrow I'm going to be shit"; la página, "a
	// wreck". La palabra literal es la marca de que estamos en el viejo.
	if strings.Contains(strings.ToLower(got.Text), "shit") {
		t.Errorf("vino la traducción literal: %q", got.Text)
	}
	t.Logf("tradujo %q", got.Text)
}

// Dictar dos cosas seguidas en el mismo idioma tiene que dar dos traducciones
// distintas: la página se reusa, y si no se la limpia bien devuelve la anterior.
func TestDosDictadosSeguidosNoRepitenLaTraduccion(t *testing.T) {
	chrome := chromeParaTraducir(t)

	primera, err := chrome.Translate("Mirá que el jueves no llego, estoy hasta las manos", "en")
	if err != nil {
		t.Fatalf("no tradujo la primera: %v", err)
	}
	segunda, err := chrome.Translate("Mañana voy a estar hecho mierda", "en")
	if err != nil {
		t.Fatalf("no tradujo la segunda: %v", err)
	}
	if primera.Text == segunda.Text {
		t.Fatalf("las dos dieron %q", primera.Text)
	}
	t.Logf("%q · %q", primera.Text, segunda.Text)
}

// Con el modo api ni se intenta la página: es el camino rápido y literal, para
// el que lo prefiera.
func TestEnModoApiNoSeUsaLaPagina(t *testing.T) {
	if testing.Short() {
		t.Skip("lanza un Chrome de verdad y sale a internet")
	}
	if ChromeBinary("") == "" {
		t.Skip("no hay Chrome en esta máquina")
	}
	chrome := NewChrome(Options{
		STT:       config.STT{ChromeHeadless: true, ChromeReadyTimeoutS: 30},
		Translate: config.Translate{Enabled: true, Mode: "api", TimeoutS: 20},
	})
	defer chrome.Close()
	if err := chrome.Load(); err != nil {
		t.Skipf("este Chrome no levantó la página de dictado: %v", err)
	}
	got, err := chrome.Translate("Mañana voy a estar hecho mierda", "en")
	if err != nil {
		t.Fatalf("no tradujo: %v", err)
	}
	if got.Web {
		t.Error("con el modo api no tendría que abrir la página del traductor")
	}
}

// Los idiomas que se van a usar se sacan de la tabla del config, sin repetidos.
func TestLosIdiomasSalenDeLaTabla(t *testing.T) {
	got := translateLanguages(config.Translate{
		Enabled: true,
		Keys:    map[string]string{"e": "en", "i": "en", "p": "pt", "x": ""},
	})
	if len(got) != 2 || got[0] != "en" || got[1] != "pt" {
		t.Fatalf("quedaron %v", got)
	}
	// Con la traducción apagada no hay ninguna pestaña que preparar.
	if got := translateLanguages(config.Translate{Keys: map[string]string{"e": "en"}}); got != nil {
		t.Errorf("quedaron %v", got)
	}
}

// El User-Agent es la otra mitad del asunto: si dice ser headless, Google
// también sirve el traductor viejo.
func TestElUserAgentNoDiceSerHeadless(t *testing.T) {
	ua := BrowserUserAgent("google-chrome")
	if strings.Contains(strings.ToLower(ua), "headless") {
		t.Errorf("el User-Agent se delata: %q", ua)
	}
	if !strings.Contains(ua, "Chrome/") {
		t.Errorf("el User-Agent no parece de Chrome: %q", ua)
	}
}

// Cada página abierta ocupa lo suyo, así que no se acumulan: al abrir la
// cuarta se cierra la que hace más que no se usa.
func TestNoSeAcumulanPaginasAbiertas(t *testing.T) {
	w := newWebTranslator(0, time.Second, "es-AR", "")
	// Sin navegador de por medio: lo que se prueba es la contabilidad de cuál
	// se cierra, no el viaje a la página.
	for _, idioma := range []string{"en", "pt", "fr"} {
		w.tabs[idioma] = &webTab{}
		w.usarLocked(idioma)
	}
	w.usarLocked("en") // el inglés vuelve a ser el más reciente
	w.tabs["it"] = &webTab{}
	w.usarLocked("it")
	w.podarLocked()

	if len(w.tabs) != maxTabs {
		t.Fatalf("quedaron %d páginas abiertas", len(w.tabs))
	}
	if _, ok := w.tabs["pt"]; ok {
		t.Error("cerró la equivocada: la más vieja era el portugués")
	}
	if _, ok := w.tabs["en"]; !ok {
		t.Error("cerró el inglés, que era el más reciente")
	}
}
