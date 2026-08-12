package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// run corre la CLI como lo haría la terminal y devuelve lo que escribió.
func run(t *testing.T, args ...string) (code int, stdout, stderr string) {
	t.Helper()
	var out, errs bytes.Buffer
	code = Main(args, &out, &errs)
	return code, out.String(), errs.String()
}

func TestSinComandoLaAyudaExplicaLosComandos(t *testing.T) {
	code, stdout, _ := run(t, "help")
	if code != 0 {
		t.Errorf("exit = %d", code)
	}
	for _, command := range []string{"run", "once", "bench", "doctor", "keys", "config", "history", "service"} {
		if !strings.Contains(stdout, command) {
			t.Errorf("la ayuda no menciona %q", command)
		}
	}
}

func TestUnComandoQueNoExisteEsErrorDeUso(t *testing.T) {
	code, _, stderr := run(t, "dictame")
	if code != 2 {
		t.Errorf("exit = %d, quería 2 (error de uso)", code)
	}
	if !strings.Contains(stderr, "dictame") {
		t.Errorf("tendría que nombrar el comando: %q", stderr)
	}
}

func TestVersionSaleEnLasDosFormas(t *testing.T) {
	for _, args := range [][]string{{"version"}, {"--version"}} {
		code, stdout, _ := run(t, args...)
		if code != 0 || !strings.Contains(stdout, "dictador") {
			t.Errorf("%v → %d %q", args, code, stdout)
		}
	}
}

func TestConfigPathDiceDondeVaElArchivo(t *testing.T) {
	base := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", base)
	code, stdout, _ := run(t, "config", "path")
	if code != 0 {
		t.Fatalf("exit = %d", code)
	}
	want := filepath.Join(base, "dictador", "config.toml")
	if strings.TrimSpace(stdout) != want {
		t.Errorf("dijo %q, quería %q", strings.TrimSpace(stdout), want)
	}
}

func TestConfigInitDejaLaPlantilla(t *testing.T) {
	base := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", base)
	code, _, _ := run(t, "config", "init")
	if code != 0 {
		t.Fatalf("exit = %d", code)
	}
	raw, err := os.ReadFile(filepath.Join(base, "dictador", "config.toml"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), "[hotkey]") {
		t.Errorf("no escribió la plantilla:\n%s", raw)
	}
}

func TestConfigSetEscribeSinPisarLosComentarios(t *testing.T) {
	base := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", base)
	if code, _, _ := run(t, "config", "init"); code != 0 {
		t.Fatal("no pude inicializar")
	}
	path := filepath.Join(base, "dictador", "config.toml")

	code, _, stderr := run(t, "config", "set", "stt.engine", "chrome")
	if code != 0 {
		t.Fatalf("exit = %d: %s", code, stderr)
	}
	raw, _ := os.ReadFile(path)
	body := string(raw)
	if !strings.Contains(body, `engine = "chrome"`) {
		t.Errorf("no escribió el valor:\n%s", body)
	}
	if !strings.Contains(body, "# faster-whisper transcribe en esta máquina") {
		t.Errorf("se comió los comentarios:\n%s", body)
	}
}

func TestConfigSetAdivinaElTipoDelValor(t *testing.T) {
	base := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", base)
	run(t, "config", "init")

	for _, c := range []struct {
		key, value, want string
	}{
		{"hotkey.cancel_on_other_key", "false", "cancel_on_other_key = false"},
		{"hotkey.hold_threshold_ms", "250", "hold_threshold_ms = 250"},
		{"limits.min_seconds", "0.5", "min_seconds = 0.5"},
		{"hotkey.key", "Menu", `key = "Menu"`},
	} {
		if code, _, stderr := run(t, "config", "set", c.key, c.value); code != 0 {
			t.Fatalf("%s: exit %d %s", c.key, code, stderr)
		}
		raw, _ := os.ReadFile(filepath.Join(base, "dictador", "config.toml"))
		if !strings.Contains(string(raw), c.want) {
			t.Errorf("falta %q en el archivo", c.want)
		}
	}
}

func TestConfigSetSeQuejaDeLaClaveMalEscrita(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	code, _, stderr := run(t, "config", "set", "engine", "chrome")
	if code != 2 {
		t.Errorf("exit = %d, quería 2", code)
	}
	if !strings.Contains(stderr, "sección.clave") {
		t.Errorf("tendría que explicar el formato: %q", stderr)
	}
	if code, _, _ := run(t, "config", "set", "stt.engine"); code != 2 {
		t.Errorf("sin valor también es error de uso, dio %d", code)
	}
}

func TestConfigShowSaleEnJSON(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	code, stdout, _ := run(t, "config", "show")
	if code != 0 {
		t.Fatalf("exit = %d", code)
	}
	var parsed map[string]any
	if err := json.Unmarshal([]byte(stdout), &parsed); err != nil {
		t.Fatalf("no es JSON: %v\n%s", err, stdout)
	}
	if _, ok := parsed["Hotkey"]; !ok {
		t.Errorf("falta la sección hotkey: %v", parsed)
	}
}

func TestServiceInstalaYDesinstalaElAutostart(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	path := filepath.Join(home, ".config", "autostart", "dictador.desktop")

	if code, stdout, _ := run(t, "service", "status"); code != 0 ||
		!strings.Contains(stdout, "no instalado") {
		t.Errorf("recién empezado tendría que estar sin instalar: %q", stdout)
	}
	if code, _, stderr := run(t, "service", "install"); code != 0 {
		t.Fatalf("install: %d %s", code, stderr)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), "Exec=") || !strings.Contains(string(raw), " run") {
		t.Errorf("el .desktop no arranca el daemon:\n%s", raw)
	}
	if code, stdout, _ := run(t, "service", "status"); code != 0 ||
		!strings.Contains(stdout, "instalado") {
		t.Errorf("status = %q", stdout)
	}
	if code, _, _ := run(t, "service", "uninstall"); code != 0 {
		t.Error("uninstall falló")
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Error("el .desktop tendría que estar borrado")
	}
	// Desinstalar dos veces no es un error.
	if code, _, _ := run(t, "service", "uninstall"); code != 0 {
		t.Error("desinstalar lo que no está tendría que salir bien")
	}
}

func TestServiceJSONEsConsumiblePorUnScript(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	code, stdout, _ := run(t, "--json", "service", "status")
	if code != 0 {
		t.Fatalf("exit = %d", code)
	}
	var parsed struct {
		Installed bool   `json:"installed"`
		Path      string `json:"path"`
	}
	if err := json.Unmarshal([]byte(stdout), &parsed); err != nil {
		t.Fatalf("no es JSON: %v\n%s", err, stdout)
	}
	if parsed.Installed {
		t.Error("no tendría que estar instalado")
	}
	if parsed.Path == "" {
		t.Error("el JSON tendría que decir dónde iría el archivo")
	}
}

func TestHistorySinNadaDictadoAvisaYFalla(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	code, _, stderr := run(t, "history")
	if code != 1 {
		t.Errorf("exit = %d, quería 1", code)
	}
	if !strings.Contains(stderr, "todavía no dictaste nada") {
		t.Errorf("stderr = %q", stderr)
	}
}

func TestParseValueDistingueTipos(t *testing.T) {
	if v := parseValue("true"); v != true {
		t.Errorf("true → %v (%T)", v, v)
	}
	if v := parseValue(" FALSE "); v != false {
		t.Errorf("FALSE → %v (%T)", v, v)
	}
	if v := parseValue("42"); v != 42 {
		t.Errorf("42 → %v (%T)", v, v)
	}
	if v := parseValue("0.35"); v != 0.35 {
		t.Errorf("0.35 → %v (%T)", v, v)
	}
	if v := parseValue("AltGr+Control_R"); v != "AltGr+Control_R" {
		t.Errorf("una tecla → %v (%T)", v, v)
	}
}

// TestTodosLosSubcomandosRegistranSusFlags corre cada uno con -h, que hace que
// el FlagSet se arme entero y no se ejecute nada. Sin esto, un flag repetido
// entre los globales y los del subcomando revienta recién cuando alguien usa
// ese comando — que fue exactamente lo que pasó con `once -q`.
func TestTodosLosSubcomandosRegistranSusFlags(t *testing.T) {
	for _, command := range []string{
		"run", "once", "bench", "doctor", "keys", "config", "history", "service",
	} {
		func() {
			defer func() {
				if r := recover(); r != nil {
					t.Errorf("%s explotó al armar sus flags: %v", command, r)
				}
			}()
			if code, _, _ := run(t, command, "-h"); code != 2 {
				t.Errorf("%s -h → %d, quería 2", command, code)
			}
		}()
	}
}
