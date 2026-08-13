package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// desktopHome deja al comando trabajando adentro de un HOME descartable, con
// una carpeta de escritorio propia: sin esto la prueba le llenaría el
// escritorio de lanzadores al que la corre.
func desktopHome(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	t.Setenv("XDG_DATA_HOME", filepath.Join(home, ".local", "share"))
	t.Setenv("XDG_DESKTOP_DIR", filepath.Join(home, "Desktop"))
	t.Setenv("XDG_CURRENT_DESKTOP", "KDE")
	if err := os.MkdirAll(filepath.Join(home, "Desktop"), 0o755); err != nil {
		t.Fatal(err)
	}
	return home
}

func TestDesktopPoneYSacaElIcono(t *testing.T) {
	home := desktopHome(t)
	shortcut := filepath.Join(home, "Desktop", "dictador.desktop")

	if code, stdout, _ := run(t, "desktop", "status"); code != 0 ||
		!strings.Contains(stdout, "no hay ícono") {
		t.Errorf("recién empezado no tendría que haber ícono: %q", stdout)
	}
	if code, _, stderr := run(t, "desktop", "install"); code != 0 {
		t.Fatalf("install: %d %s", code, stderr)
	}
	entry, err := os.ReadFile(shortcut)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(entry), "[Desktop Entry]") ||
		!strings.Contains(string(entry), " run --notify\n") {
		t.Errorf("el lanzador no arranca el dictado:\n%s", entry)
	}
	if _, err := os.Stat(filepath.Join(home, ".local", "share", "applications", "dictador.desktop")); err != nil {
		t.Errorf("no quedó la entrada del menú: %v", err)
	}
	if code, stdout, _ := run(t, "desktop", "status"); code != 0 ||
		!strings.Contains(stdout, "KDE Plasma") {
		t.Errorf("status = %q", stdout)
	}
	if code, _, _ := run(t, "desktop", "uninstall"); code != 0 {
		t.Error("uninstall falló")
	}
	if _, err := os.Stat(shortcut); !os.IsNotExist(err) {
		t.Error("el ícono tendría que estar borrado")
	}
}

func TestDesktopJSONEsConsumiblePorUnScript(t *testing.T) {
	desktopHome(t)
	code, stdout, _ := run(t, "--json", "desktop", "status")
	if code != 0 {
		t.Fatalf("exit = %d", code)
	}
	var parsed struct {
		Installed bool   `json:"installed"`
		Shortcut  string `json:"shortcut"`
		Desktop   string `json:"desktop"`
	}
	if err := json.Unmarshal([]byte(stdout), &parsed); err != nil {
		t.Fatalf("no es JSON: %v\n%s", err, stdout)
	}
	if parsed.Installed {
		t.Error("no tendría que estar instalado")
	}
	if parsed.Shortcut == "" || parsed.Desktop == "" {
		t.Errorf("el JSON tendría que decir dónde iría y en qué escritorio: %+v", parsed)
	}
}

func TestDesktopSeQuejaDeLoQueNoConoce(t *testing.T) {
	desktopHome(t)
	code, _, stderr := run(t, "desktop", "instalar")
	if code != 2 {
		t.Errorf("exit = %d, quería 2 (error de uso)", code)
	}
	if !strings.Contains(stderr, "install") {
		t.Errorf("tendría que decir qué acciones hay: %q", stderr)
	}
}
