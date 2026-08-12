// Package stt son los motores de reconocimiento de voz.
//
// La interfaz es a propósito mínima —cargar y transcribir— para poder enchufar
// otro motor sin tocar el resto de la app. La única distinción que sí importa
// es entre los motores que reciben el audio grabado (Whisper, Google) y los que
// escuchan el micrófono ellos mismos en tiempo real (Chrome): esos últimos
// implementan LiveEngine y el daemon les avisa cuándo empieza y cuándo termina
// el dictado en vez de pasarles un array de samples.
package stt

import (
	"fmt"
	"math"
	"os"
	"os/exec"
	"strings"

	"github.com/neitanod/dictador/internal/config"
)

// Engine es lo que la app le pide a cualquier motor.
type Engine interface {
	// Name es el identificador corto: "whisper", "chrome", "google".
	Name() string
	// Describe es la línea que ve el usuario en el log y en la config.
	Describe() string
	// Load deja el motor listo para transcribir (baja modelos, abre procesos).
	Load() error
	// Transcribe devuelve el texto del audio. Con partial = true se pide la
	// versión rápida y descartable que se dibuja mientras hablás.
	Transcribe(audio []float32, partial bool) (string, error)
	// SupportsPartial es false para los motores que cobran cada llamada.
	SupportsPartial() bool
	// Close suelta lo que el motor tenga tomado (procesos, sockets).
	Close()
}

// LiveEngine es un motor que escucha el micrófono por su cuenta.
//
// El daemon mira si el motor lo implementa: si sí, le avisa el arranque y el
// final del dictado; si no, le pasa el audio que grabó él.
type LiveEngine interface {
	Engine
	// StartLive: empezó el dictado, abrí el micrófono.
	StartLive() error
	// AbortLive: se canceló, cerrá el micrófono y tirá lo que oíste.
	AbortLive()
	// PartialText es lo que lleva escuchado hasta ahora.
	PartialText() string
	// FinishLive: se soltó la tecla, dame el texto definitivo.
	FinishLive() (string, error)
}

// Error es un problema del motor que se le puede mostrar al usuario.
type Error struct{ msg string }

func (e *Error) Error() string { return e.msg }

func errf(format string, args ...any) error {
	return &Error{msg: fmt.Sprintf(format, args...)}
}

// aliases lleva los nombres que la gente escribe al identificador real.
var aliases = map[string]string{
	"faster-whisper": "whisper",
	"whisper":        "whisper",
	"local":          "whisper",
	"chrome":         "chrome",
	"web-speech":     "chrome",
	"webspeech":      "chrome",
	"google":         "google",
	"google-cloud":   "google",
	"gcloud":         "google",
}

// Labels es cómo se llama cada motor en la ventana de configuración.
var Labels = map[string]string{
	"whisper": "Whisper local",
	"chrome":  "Chrome / Web Speech",
	"google":  "Google Speech-to-Text",
}

// Canonical resuelve el nombre de un motor.
func Canonical(engine string) (string, error) {
	key := strings.ToLower(strings.TrimSpace(engine))
	name, ok := aliases[key]
	if !ok {
		return "", errf("motor de voz desconocido: %q", engine)
	}
	return name, nil
}

// googleLocales es el BCP-47 que espera Google a partir del código corto que
// usa Whisper. Rioplatense para el castellano, porque es la voz que dicta acá.
var googleLocales = map[string]string{
	"es": "es-AR",
	"en": "en-US",
	"pt": "pt-BR",
	"fr": "fr-FR",
	"it": "it-IT",
	"de": "de-DE",
}

// GoogleLocale es el idioma en BCP-47, derivado del corto si no lo fijaste.
func GoogleLocale(cfg config.STT) string {
	if explicit := strings.TrimSpace(cfg.GoogleLanguage); explicit != "" {
		return explicit
	}
	short := strings.TrimSpace(cfg.Language)
	if short == "" {
		short = "es"
	}
	if locale, ok := googleLocales[strings.ToLower(strings.Split(short, "-")[0])]; ok {
		return locale
	}
	// Un idioma que no está en el mapa y ya viene completo se pasa tal cual.
	if strings.Contains(short, "-") {
		return short
	}
	return "es-AR"
}

// GoogleAPIKey es la key del config, y si no está, la del entorno.
//
// El entorno gana como escape para no dejar la key escrita en un archivo, que
// es lo que uno quiere en una máquina compartida.
func GoogleAPIKey(cfg config.STT) string {
	for _, name := range []string{"DICTADOR_GOOGLE_API_KEY", "DICTADO_GOOGLE_API_KEY", "GOOGLE_API_KEY"} {
		if v := strings.TrimSpace(os.Getenv(name)); v != "" {
			return v
		}
	}
	return strings.TrimSpace(cfg.GoogleAPIKey)
}

// NormalizeGain sube el volumen de una grabación floja.
//
// Whisper se come muchas más palabras con audio bajo que con audio ruidoso, y
// los micrófonos de laptop suelen entregar picos de 0.05. El techo de ganancia
// está para no amplificar el ruido de una toma que en realidad es silencio.
func NormalizeGain(audio []float32, target, maxGain float64) []float32 {
	if len(audio) == 0 {
		return audio
	}
	peak := 0.0
	for _, s := range audio {
		if v := math.Abs(float64(s)); v > peak {
			peak = v
		}
	}
	if peak < 1e-4 || peak >= target {
		return audio
	}
	gain := math.Min(maxGain, target/peak)
	out := make([]float32, len(audio))
	for i, s := range audio {
		out[i] = float32(float64(s) * gain)
	}
	return out
}

// Options es lo que un motor necesita para armarse: la sección [stt] más el
// sample rate y el device reales de [audio], que Google y Chrome necesitan.
type Options struct {
	STT        config.STT
	SampleRate int
	Device     string
}

// OptionsFrom arma las opciones desde la configuración entera.
func OptionsFrom(cfg config.Config) Options {
	return Options{STT: cfg.STT, SampleRate: cfg.Audio.SampleRate, Device: cfg.Audio.Device}
}

// Build arma el motor que dice la configuración.
func Build(opts Options) (Engine, error) {
	name, err := Canonical(opts.STT.Engine)
	if err != nil {
		return nil, err
	}
	switch name {
	case "chrome":
		return NewChrome(opts), nil
	case "google":
		return NewGoogle(opts), nil
	default:
		return NewWhisper(opts), nil
	}
}

// Available son los motores que esta máquina puede usar hoy.
//
// Google está siempre (le falta una key, no un programa); Chrome depende de que
// haya un Chrome en el PATH y Whisper de que haya un whisper-server escuchando.
func Available(opts Options) []string {
	var out []string
	if WhisperAvailable(opts) {
		out = append(out, "whisper")
	}
	if ChromeAvailable(opts.STT.ChromeBinary) {
		out = append(out, "chrome")
	}
	return append(out, "google")
}

// lookPath es exec.LookPath, aparte para poder pisarlo en los tests.
var lookPath = exec.LookPath
