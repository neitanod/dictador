// Package config carga y guarda ~/.config/dictador/config.toml.
//
// El archivo está lleno de comentarios que explican cada valor, así que guardar
// no vuelca la estructura entera: edita las líneas que cambian y deja el resto
// intacto (ver save.go).
package config

import (
	"os"
	"path/filepath"

	"github.com/BurntSushi/toml"
)

// Hotkey: qué tecla dispara el dictado y cómo se comporta.
type Hotkey struct {
	// Lo último es la tecla gatillo, lo de antes tiene que estar apretado.
	// Control_R es la tecla Copilot de la laptop, que keyd remapea a
	// rightcontrol, y va con AltGr porque esa tecla sola se usa para otras cosas.
	Key string `toml:"key"`
	// hold = push-to-talk (mantener apretada) | toggle = apretar para arrancar/cortar
	Mode string `toml:"mode"`
	// Milisegundos que hay que mantenerla antes de empezar a grabar, para no
	// dictar cuando la usás como modificador de verdad.
	HoldThresholdMs int `toml:"hold_threshold_ms"`
	// Si mientras está apretada llega otra tecla, se cancela el dictado.
	CancelOnOtherKey bool `toml:"cancel_on_other_key"`
}

// Audio: de dónde y cómo se graba.
type Audio struct {
	Device     string `toml:"device"`      // vacío = fuente por default de PipeWire/Pulse
	SampleRate int    `toml:"sample_rate"` // lo que espera Whisper
}

// STT: el motor de reconocimiento y lo que cada uno necesita.
type STT struct {
	// faster-whisper = local y offline | chrome = Web Speech, gratis por
	// internet | google = Cloud Speech-to-Text, paga
	Engine string `toml:"engine"`

	ChromeBinary        string  `toml:"chrome_binary"`          // vacío = el primer Chrome del PATH
	ChromeLanguage      string  `toml:"chrome_language"`        // "" = derivado de language
	ChromeHeadless      bool    `toml:"chrome_headless"`        // false para ver la ventana y depurar
	ChromeReadyTimeoutS float64 `toml:"chrome_ready_timeout_s"` //
	ChromeFinalTimeoutS float64 `toml:"chrome_final_timeout_s"` //

	GoogleAPIKey      string  `toml:"google_api_key"`     // sólo para engine = "google"
	GoogleLanguage    string  `toml:"google_language"`    // "" = derivado de language (es → es-AR)
	GoogleModel       string  `toml:"google_model"`       //
	GooglePunctuation bool    `toml:"google_punctuation"` //
	GoogleTimeoutS    float64 `toml:"google_timeout_s"`   //

	Model             string `toml:"model"`               // tiny | base | small | medium | large-v3
	PartialModel      string `toml:"partial_model"`       // el chico que dibuja el texto en vivo
	Language          string `toml:"language"`            // "" = autodetectar
	Device            string `toml:"device"`              // cpu | cuda
	ComputeType       string `toml:"compute_type"`        //
	CPUThreads        int    `toml:"cpu_threads"`         // 0 = min(16, cores)
	BeamSize          int    `toml:"beam_size"`           // pasada final
	PartialIntervalMs int    `toml:"partial_interval_ms"` //
	InitialPrompt     string `toml:"initial_prompt"`      // sesga jerga y nombres propios
	VadFilter         bool   `toml:"vad_filter"`          //
	AutoGain          bool   `toml:"auto_gain"`           // levanta el volumen de tomas flojas
	WhisperServerURL  string `toml:"whisper_server_url"`  // whisper.cpp residente por HTTP local
}

// Action: qué se hace con el texto cuando el dictado termina.
type Action struct {
	OnRelease        string `toml:"on_release"` // paste | type | clipboard | keep_open
	RestoreFocus     bool   `toml:"restore_focus"`
	TrailingSpace    bool   `toml:"trailing_space"`
	StripFinalPeriod bool   `toml:"strip_final_period"`
}

// Overlay: la ventanita que muestra lo que vas diciendo.
type Overlay struct {
	Enabled     bool   `toml:"enabled"`
	Position    string `toml:"position"` // bottom-center | top-center | center
	FontSize    int    `toml:"font_size"`
	Width       int    `toml:"width"`
	Margin      int    `toml:"margin"`
	HideDelayMs int    `toml:"hide_delay_ms"`
}

// Limits: los topes del dictado.
type Limits struct {
	MaxSeconds float64 `toml:"max_seconds"`
	MinSeconds float64 `toml:"min_seconds"` // menos que esto se descarta como pulsación accidental
}

// Config es el config.toml entero, ya con los defaults aplicados.
type Config struct {
	Hotkey  Hotkey  `toml:"hotkey"`
	Audio   Audio   `toml:"audio"`
	STT     STT     `toml:"stt"`
	Action  Action  `toml:"action"`
	Overlay Overlay `toml:"overlay"`
	Limits  Limits  `toml:"limits"`

	// Path es el archivo del que salió, o "" si son los defaults pelados.
	Path string `toml:"-" json:"-"`
}

// Defaults devuelve la configuración con la que la app anda sin archivo.
func Defaults() Config {
	return Config{
		Hotkey: Hotkey{
			Key:              "AltGr+Control_R",
			Mode:             "hold",
			HoldThresholdMs:  180,
			CancelOnOtherKey: true,
		},
		Audio: Audio{
			Device:     "",
			SampleRate: 16000,
		},
		STT: STT{
			Engine:              "faster-whisper",
			ChromeHeadless:      true,
			ChromeReadyTimeoutS: 25,
			ChromeFinalTimeoutS: 6,
			GoogleModel:         "latest_long",
			GooglePunctuation:   true,
			GoogleTimeoutS:      25,
			Model:               "small",
			PartialModel:        "tiny",
			Language:            "es",
			Device:              "cpu",
			ComputeType:         "int8",
			CPUThreads:          0,
			BeamSize:            1,
			PartialIntervalMs:   900,
			VadFilter:           true,
			AutoGain:            true,
			WhisperServerURL:    "http://127.0.0.1:8080",
		},
		Action: Action{
			OnRelease:    "paste",
			RestoreFocus: true,
		},
		Overlay: Overlay{
			Enabled:     true,
			Position:    "bottom-center",
			FontSize:    19,
			Width:       780,
			Margin:      90,
			HideDelayMs: 1400,
		},
		Limits: Limits{
			MaxSeconds: 120,
			MinSeconds: 0.35,
		},
	}
}

// ConfigDir es donde vive el config.toml de esta app.
func ConfigDir() string {
	base := os.Getenv("XDG_CONFIG_HOME")
	if base == "" {
		base = filepath.Join(home(), ".config")
	}
	return filepath.Join(base, "dictador")
}

// ConfigPath es el archivo que se lee y se escribe por default.
func ConfigPath() string { return filepath.Join(ConfigDir(), "config.toml") }

// LegacyConfigPath es el config del `dictado` en Python.
//
// Se lee sólo si el de dictador todavía no existe, para que el port arranque
// con la configuración que la máquina ya tenía en vez de con los defaults. En
// cuanto guardás algo, el archivo nuevo pasa a mandar.
func LegacyConfigPath() string {
	base := os.Getenv("XDG_CONFIG_HOME")
	if base == "" {
		base = filepath.Join(home(), ".config")
	}
	return filepath.Join(base, "dictado", "config.toml")
}

// StateDir guarda el historial y lo que la app necesite recordar.
func StateDir() string {
	base := os.Getenv("XDG_STATE_HOME")
	if base == "" {
		base = filepath.Join(home(), ".local", "share")
	}
	return filepath.Join(base, "dictador")
}

func home() string {
	h, err := os.UserHomeDir()
	if err != nil {
		return "."
	}
	return h
}

// Load lee el config.toml y le aplica los defaults a lo que no esté.
//
// Con path vacío usa el de la app, y si ese no existe cae al del dictado en
// Python. Que el archivo no exista no es un error: se anda con los defaults.
func Load(path string) (Config, error) {
	cfg := Defaults()
	explicit := path != ""
	if !explicit {
		path = ConfigPath()
		if !exists(path) {
			if legacy := LegacyConfigPath(); exists(legacy) {
				path = legacy
			}
		}
	}
	if !exists(path) {
		if explicit {
			return cfg, &os.PathError{Op: "open", Path: path, Err: os.ErrNotExist}
		}
		return cfg, nil
	}
	// BurntSushi decodifica sobre la estructura que le das, así que lo que el
	// archivo no menciona se queda con el default.
	if _, err := toml.DecodeFile(path, &cfg); err != nil {
		return cfg, err
	}
	cfg.Path = path
	return cfg, nil
}

func exists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}
