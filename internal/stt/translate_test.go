package stt

import (
	"strings"
	"testing"
	"time"

	"github.com/neitanod/dictador/internal/config"
)

// La traducción sale del mismo Chrome que escucha, hablando con el traductor
// de Google sin API key. Que eso funcione no se puede afirmar leyendo el
// código: depende de que el endpoint conteste con CORS abierto a una página
// servida desde localhost, así que el test levanta un Chrome de verdad.
func TestChromeTraduceConGoogle(t *testing.T) {
	if testing.Short() {
		t.Skip("lanza un Chrome de verdad y sale a internet")
	}
	if ChromeBinary("") == "" {
		t.Skip("no hay Chrome en esta máquina")
	}

	chrome := NewChrome(Options{
		STT:       config.STT{ChromeHeadless: true, ChromeReadyTimeoutS: 30},
		Translate: config.Translate{TimeoutS: 20},
	})
	defer chrome.Close()

	if err := chrome.Load(); err != nil {
		t.Skipf("este Chrome no levantó la página de dictado: %v", err)
	}

	got, err := chrome.Translate("hola, ¿cómo andás? esto es una prueba", "en")
	if err != nil {
		t.Fatalf("no tradujo: %v", err)
	}
	if !strings.Contains(strings.ToLower(got.Text), "how are you") {
		t.Errorf("la traducción quedó rara: %q", got.Text)
	}
	// El idioma de origen se detecta solo, que es lo que hace que dictar en
	// inglés y pedir portugués también ande.
	if got.From != "es" {
		t.Errorf("detectó %q como idioma de origen", got.From)
	}
}

// Un dictado largo va por POST y no en la URL: en la URL entra hasta cierto
// punto y después el endpoint contesta un error que no dice nada.
func TestChromeTraduceUnDictadoLargo(t *testing.T) {
	if testing.Short() {
		t.Skip("lanza un Chrome de verdad y sale a internet")
	}
	if ChromeBinary("") == "" {
		t.Skip("no hay Chrome en esta máquina")
	}

	chrome := NewChrome(Options{
		STT:       config.STT{ChromeHeadless: true, ChromeReadyTimeoutS: 30},
		Translate: config.Translate{TimeoutS: 30},
	})
	defer chrome.Close()
	if err := chrome.Load(); err != nil {
		t.Skipf("este Chrome no levantó la página de dictado: %v", err)
	}

	largo := strings.Repeat("esta es una oración de prueba para hacerlo largo. ", 60)
	got, err := chrome.Translate(largo, "en")
	if err != nil {
		t.Fatalf("no tradujo %d caracteres: %v", len(largo), err)
	}
	if len(got.Text) < len(largo)/3 {
		t.Errorf("se perdió texto por el camino: %d caracteres de %d", len(got.Text), len(largo))
	}
}

// Un texto vacío o sin idioma no es un error: es un dictado que no tiene nada
// que traducir, y sale por donde entró.
func TestTraducirNadaNoEsUnError(t *testing.T) {
	chrome := NewChrome(Options{})
	got, err := chrome.Translate("   ", "en")
	if err != nil || got.Text != "" {
		t.Fatalf("dio %q, %v", got.Text, err)
	}
	if got, err := chrome.Translate("hola", ""); err != nil || got.Text != "hola" {
		t.Fatalf("dio %q, %v", got.Text, err)
	}
}

// Una respuesta que llega tarde —después de que el dictado se pegó sin ella—
// se tira sin molestar a nadie.
func TestLaTraducciónQueLlegaTardeSeTira(t *testing.T) {
	chrome := NewChrome(Options{})
	// Nadie esperando ese id: tiene que salir sin colgarse ni romper.
	chrome.onTranslation("translation", "t99", "Hello", "es")

	answers := make(chan translationResult, 1)
	chrome.jobs["t1"] = answers
	chrome.onTranslation("translation-error", "t1", "TypeError: failed to fetch", "")
	select {
	case answer := <-answers:
		if answer.err == "" {
			t.Error("el error tendría que haber llegado como error")
		}
	case <-time.After(time.Second):
		t.Error("la respuesta no llegó a quien la esperaba")
	}
}

func TestSóloChromeSabeTraducir(t *testing.T) {
	if !CanTranslate(NewChrome(Options{})) {
		t.Error("Chrome tendría que saber traducir")
	}
	if CanTranslate(NewWhisper(Options{})) {
		t.Error("Whisper transcribe local: no tiene con qué traducir")
	}
	if CanTranslate(NewGoogle(Options{})) {
		t.Error("con Google Cloud cada llamada se factura: no se ofrece")
	}
}
