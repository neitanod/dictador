package cli

import (
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/neitanod/dictador/internal/audio"
	"github.com/neitanod/dictador/internal/config"
	"github.com/neitanod/dictador/internal/overlay"
	"github.com/neitanod/dictador/internal/stt"
	"github.com/neitanod/dictador/internal/translate"
	"github.com/neitanod/dictador/internal/x11"
)

// Check es un chequeo del doctor.
type Check struct {
	Name   string `json:"name"`
	OK     bool   `json:"ok"`
	Detail string `json:"detail"`
}

// DoctorReport es todo lo que el doctor tiene para decir.
type DoctorReport struct {
	OK         bool    `json:"ok"`
	Checks     []Check `json:"checks"`
	ConfigPath string  `json:"config_path"`
	Action     string  `json:"action"`
	Mode       string  `json:"mode"`
}

func cmdDoctor(opts *options, args []string) int {
	fs := subflags("doctor", opts, opts.out.stderr)
	if err := fs.Parse(args); err != nil {
		return 2
	}
	opts.refresh()
	cfg, err := opts.load()
	if err != nil {
		opts.out.fail(err, "CONFIG")
		return 1
	}

	var checks []Check
	add := func(name string, ok bool, detail string) {
		checks = append(checks, Check{Name: name, OK: ok, Detail: detail})
	}

	session := os.Getenv("XDG_SESSION_TYPE")
	if session == "" {
		session = "?"
	}
	add("sesión X11", session == "x11", "XDG_SESSION_TYPE="+session)
	display := os.Getenv("DISPLAY")
	add("DISPLAY", display != "", orElse(display, "sin definir"))

	add("binario parec", audio.Available(), which("parec", "falta pulseaudio-utils"))
	add("notificaciones", overlay.NotifyAvailable(), which("notify-send", "falta libnotify-bin: la ventanita no se va a ver"))

	add("micrófono", microphone() != "", orElse(microphone(), "no encontré fuentes de entrada"))

	// El mapa de teclado se guarda para el chequeo de la traducción, que va más
	// abajo porque además depende del motor.
	var keymap *x11.Keymap

	// La conexión X sirve de una vez para la tecla, XInput2 y XTEST.
	conn, xerr := x11.Open()
	if xerr != nil {
		add("conexión X11", false, xerr.Error())
		add("XInput2", false, "sin conexión X no se puede saber")
		add("XTEST", false, "sin conexión X no se puede saber")
		add("tecla "+cfg.Hotkey.Key, false, "sin conexión X no se puede saber")
	} else {
		defer conn.Close()
		add("conexión X11", true, display)

		// Dónde aparece la ventanita es la clase de cosa que uno mira acá
		// cuando aparece en la pantalla equivocada.
		monitors := conn.Monitors()
		names := make([]string, 0, len(monitors))
		for _, m := range monitors {
			label := fmt.Sprintf("%s %dx%d en (%d,%d)", m.Name, m.Width, m.Height, m.X, m.Y)
			if m.Primary {
				label += " principal"
			}
			names = append(names, label)
		}
		add(fmt.Sprintf("pantallas (%d)", len(monitors)), len(monitors) > 0,
			strings.Join(names, " · "))
		// El DPI decide el tamaño de la letra del overlay: a 72 se vería un
		// cuarto más chica que el resto del escritorio.
		add("resolución de la letra", true,
			fmt.Sprintf("%.0f DPI · %d puntos", conn.DPI(), cfg.Overlay.FontSize))
		add("la ventanita aparece", true, describePlacement(conn, cfg.Overlay, monitors))

		if _, err := conn.EnableXInput(); err != nil {
			add("XInput2", false, err.Error())
		} else {
			add("XInput2", true, "la tecla se escucha sin agarrarla")
		}
		if err := conn.EnableXTest(); err != nil {
			add("XTEST", false, err.Error())
		} else {
			add("XTEST", true, "el pegado va sin xdotool")
		}
		keymap, err = conn.LoadKeymap()
		if err != nil {
			add("tecla "+cfg.Hotkey.Key, false, err.Error())
		} else if combo, err := x11.ParseCombo(cfg.Hotkey.Key, keymap); err != nil {
			add("tecla "+cfg.Hotkey.Key, false, err.Error())
		} else {
			add("tecla "+cfg.Hotkey.Key, true, combo.Describe())
		}
	}

	// Motor de voz
	engineOpts := stt.OptionsFrom(cfg)
	engine, err := stt.Build(engineOpts)
	if err != nil {
		add("motor "+cfg.STT.Engine, false, err.Error())
	} else {
		add("motor "+cfg.STT.Engine, true, engine.Describe())
		defer engine.Close()
		switch engine.Name() {
		case "google":
			has := stt.GoogleAPIKey(cfg.STT) != ""
			add("API key de Google", has, ifElse(has, "cargada",
				"falta: `dictador config set stt.google_api_key AIza…` o GOOGLE_API_KEY"))
		case "chrome":
			binary := stt.ChromeBinary(cfg.STT.ChromeBinary)
			add("Chrome", binary != "", orElse(binary,
				"no está en el PATH: instalá google-chrome"))
		case "whisper":
			up := stt.WhisperAvailable(engineOpts)
			add("whisper-server", up, ifElse(up, cfg.STT.WhisperServerURL,
				"no contesta en "+cfg.STT.WhisperServerURL+
					": levantalo con `whisper-server -m <modelo>.bin --port 8080`"))
		}
	}
	add("motores disponibles", true, strings.Join(stt.Available(engineOpts), ", "))

	// Traducción instantánea: acá se mira cuando "la e no traduce", así que
	// tiene que decir las tres cosas que pueden estar mal — apagada, motor que
	// no traduce, o una letra que este teclado no tiene.
	add("traducción", translationOK(cfg, keymap), describeTranslation(cfg, keymap))

	ok := true
	lines := make([]string, 0, len(checks)+4)
	for _, c := range checks {
		if !c.OK {
			ok = false
		}
		mark := "✓"
		if !c.OK {
			mark = "✗"
		}
		lines = append(lines, fmt.Sprintf("%s %s: %s", mark, c.Name, c.Detail))
	}
	configPath := cfg.Path
	lines = append(lines, "",
		"config: "+orElse(configPath, "defaults (no hay archivo, corré `dictador config init`)"),
		"acción al soltar: "+cfg.Action.OnRelease,
		"modo: "+cfg.Hotkey.Mode)

	report := DoctorReport{
		OK: ok, Checks: checks, ConfigPath: configPath,
		Action: cfg.Action.OnRelease, Mode: cfg.Hotkey.Mode,
	}
	_ = opts.out.print(report, lines)
	if !ok {
		return 1
	}
	return 0
}

// translationOK dice si la traducción está en condiciones de disparar.
//
// Apagada cuenta como bien: es una decisión, no una falla.
func translationOK(cfg config.Config, keymap *x11.Keymap) bool {
	if !cfg.Translate.Enabled {
		return true
	}
	engine, err := stt.Canonical(cfg.STT.Engine)
	if err != nil || !stt.TranslatorEngines[engine] {
		return false
	}
	return len(missingLanguageKeys(cfg, keymap)) == 0
}

// describeTranslation cuenta a qué idioma manda cada letra, y qué le falta.
func describeTranslation(cfg config.Config, keymap *x11.Keymap) string {
	if !cfg.Translate.Enabled {
		return "apagada: prendela en la configuración o con translate.enabled = true"
	}
	engine, err := stt.Canonical(cfg.STT.Engine)
	if err != nil || !stt.TranslatorEngines[engine] {
		return "el motor " + cfg.STT.Engine + " no traduce: hace falta engine = \"chrome\""
	}
	bindings := translate.Bindings(cfg.Translate.Keys)
	if len(bindings) == 0 {
		return "sin ninguna letra configurada en [translate.keys]"
	}
	parts := make([]string, 0, len(bindings))
	for _, b := range bindings {
		parts = append(parts, strings.ToUpper(b.Key)+" → "+translate.Name(b.Language))
	}
	detail := strings.Join(parts, " · ")
	if missing := missingLanguageKeys(cfg, keymap); len(missing) > 0 {
		detail += " · tu teclado no tiene " + strings.Join(missing, ", ")
	}
	return detail
}

// missingLanguageKeys son las letras configuradas que este teclado no tiene.
func missingLanguageKeys(cfg config.Config, keymap *x11.Keymap) []string {
	if keymap == nil {
		return nil // sin conexión X no se puede saber, y no es culpa de la tabla
	}
	var missing []string
	for _, b := range translate.Bindings(cfg.Translate.Keys) {
		if _, err := keymap.KeycodesFor(b.Key); err != nil {
			missing = append(missing, b.Key)
		}
	}
	return missing
}

// describePlacement cuenta en qué pantalla y en qué lugar va a aparecer el
// overlay ahora mismo, ya resuelto: "donde está el mouse" no le dice a nadie si
// eso hoy es la laptop o la tele.
func describePlacement(conn *x11.Conn, cfg config.Overlay, monitors []x11.Monitor) string {
	where := cfg.Screen
	if where == "" {
		where = "mouse"
	}
	resolved := ""
	switch where {
	case "all":
		resolved = "en todas"
	case "primary":
		if m, ok := x11.PrimaryMonitor(monitors); ok {
			resolved = m.Name
		}
	case "focus":
		if m, ok := conn.FocusMonitor(monitors); ok {
			resolved = m.Name
		}
	case "mouse":
		if m, ok := conn.PointerMonitor(monitors); ok {
			resolved = m.Name
		}
	default:
		if _, ok := x11.MonitorByName(monitors, where); ok {
			resolved = where
		} else {
			resolved = where + " (que ahora no está conectada)"
		}
	}
	position := cfg.Position
	if position == "" {
		position = "bottom-center"
	}
	if resolved == "" {
		return where + ", " + position
	}
	return fmt.Sprintf("%s → %s, %s", where, resolved, position)
}

// microphone es la primera fuente de entrada real que reporta pactl.
func microphone() string {
	out, err := exec.Command("pactl", "list", "short", "sources").Output()
	if err != nil {
		return ""
	}
	for _, row := range strings.Split(string(out), "\n") {
		if row == "" || strings.Contains(row, ".monitor") {
			continue
		}
		if fields := strings.Split(row, "\t"); len(fields) > 1 {
			return fields[1]
		}
	}
	return ""
}

func which(binary, missing string) string {
	if path, err := exec.LookPath(binary); err == nil {
		return path
	}
	return missing
}

func orElse(value, fallback string) string {
	if value == "" {
		return fallback
	}
	return value
}

func ifElse(cond bool, yes, no string) string {
	if cond {
		return yes
	}
	return no
}
