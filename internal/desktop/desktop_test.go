package desktop

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// fakeHome deja al paquete entero adentro de una carpeta descartable: todo lo
// que escribe sale de HOME, así que ninguna prueba toca el escritorio del que
// las corre.
func fakeHome(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	t.Setenv("XDG_DATA_HOME", filepath.Join(home, ".local", "share"))
	t.Setenv("XDG_DESKTOP_DIR", "")
	t.Setenv("XDG_CURRENT_DESKTOP", "")
	t.Setenv("DESKTOP_SESSION", "")
	noCommands(t)
	return home
}

// noCommands corta la salida a programas de afuera. El default de las pruebas
// es que no haya ninguno: así se prueba la máquina más pelada, que es donde
// esto se rompe.
func noCommands(t *testing.T) {
	t.Helper()
	original := run
	run = func(name string, args ...string) ([]byte, error) {
		return nil, errors.New("no existe " + name)
	}
	t.Cleanup(func() { run = original })
}

func TestNameTraduceLoQueDiceElEntorno(t *testing.T) {
	cases := map[string]string{
		"KDE":          "KDE Plasma",
		"ubuntu:GNOME": "GNOME",
		"X-Cinnamon":   "Cinnamon",
		"XFCE":         "Xfce",
		"":             "",
	}
	for raw, want := range cases {
		t.Setenv("XDG_CURRENT_DESKTOP", raw)
		t.Setenv("DESKTOP_SESSION", "")
		if got := Name(); got != want {
			t.Errorf("XDG_CURRENT_DESKTOP=%q dio %q, esperaba %q", raw, got, want)
		}
	}
}

func TestNameCaeEnLaSesionCuandoNoHayVariable(t *testing.T) {
	t.Setenv("XDG_CURRENT_DESKTOP", "")
	t.Setenv("DESKTOP_SESSION", "plasmax11")
	if got := Name(); got != "KDE Plasma" {
		t.Fatalf("dio %q, esperaba KDE Plasma", got)
	}
}

func TestDirPrefiereLaCarpetaDeclarada(t *testing.T) {
	home := fakeHome(t)
	escritorio := filepath.Join(home, "Escritorio")
	if err := os.MkdirAll(escritorio, 0o755); err != nil {
		t.Fatal(err)
	}
	config := filepath.Join(home, ".config")
	if err := os.MkdirAll(config, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(config, "user-dirs.dirs"),
		[]byte("XDG_MUSIC_DIR=\"$HOME/Música\"\nXDG_DESKTOP_DIR=\"$HOME/Escritorio\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// Existe también un ~/Desktop, que es el que se elegiría adivinando.
	if err := os.MkdirAll(filepath.Join(home, "Desktop"), 0o755); err != nil {
		t.Fatal(err)
	}
	dir, err := Dir()
	if err != nil {
		t.Fatal(err)
	}
	if dir != escritorio {
		t.Fatalf("dio %q, esperaba %q", dir, escritorio)
	}
}

func TestDirCaeEnDesktopCuandoNoHayNadaQueDiga(t *testing.T) {
	home := fakeHome(t)
	if err := os.MkdirAll(filepath.Join(home, "Desktop"), 0o755); err != nil {
		t.Fatal(err)
	}
	dir, err := Dir()
	if err != nil {
		t.Fatal(err)
	}
	if dir != filepath.Join(home, "Desktop") {
		t.Fatalf("dio %q", dir)
	}
}

func TestDirSeQuejaCuandoNoHayEscritorio(t *testing.T) {
	fakeHome(t)
	if _, err := Dir(); err == nil {
		t.Fatal("esperaba un error y no hubo")
	}
}

func TestEntryEsUnLanzadorValido(t *testing.T) {
	entry := Entry("/home/quien/.local/bin/dictador", "dictador", false)
	for _, want := range []string{
		"[Desktop Entry]",
		"Type=Application",
		"Exec=/home/quien/.local/bin/dictador run",
		"Icon=dictador",
		"Terminal=false",
		"StartupNotify=false",
		"Categories=",
	} {
		if !strings.Contains(entry, want) {
			t.Errorf("falta %q en:\n%s", want, entry)
		}
	}
	if strings.Contains(entry, "X-GNOME-Autostart-enabled") {
		t.Error("el lanzador del menú no es un autostart")
	}
}

func TestEntryDeAutostartSeDeclaraComoTal(t *testing.T) {
	entry := Entry("/bin/dictador", "", true)
	if !strings.Contains(entry, "X-GNOME-Autostart-enabled=true") {
		t.Error("al autostart le falta la línea que GNOME necesita")
	}
	if !strings.Contains(entry, "Icon="+fallbackIcon) {
		t.Error("sin ícono propio tiene que ir el del sistema")
	}
}

func TestEntryEntrecomillaLasRutasConEspacios(t *testing.T) {
	entry := Entry("/home/quien/mis programas/dictador", "dictador", false)
	if !strings.Contains(entry, `Exec="/home/quien/mis programas/dictador" run`) {
		t.Fatalf("la ruta con espacios quedó partida:\n%s", entry)
	}
}

func TestInstallDejaLasTresCosasEnSuLugar(t *testing.T) {
	home := fakeHome(t)
	t.Setenv("XDG_CURRENT_DESKTOP", "KDE")
	desk := filepath.Join(home, "Desktop")
	if err := os.MkdirAll(desk, 0o755); err != nil {
		t.Fatal(err)
	}

	result, err := Install("/usr/local/bin/dictador")
	if err != nil {
		t.Fatal(err)
	}
	if result.Desktop != "KDE Plasma" {
		t.Errorf("detectó %q", result.Desktop)
	}
	if result.Shortcut != filepath.Join(desk, fileName) {
		t.Errorf("el ícono fue a %q", result.Shortcut)
	}

	shortcut, err := os.Stat(result.Shortcut)
	if err != nil {
		t.Fatal(err)
	}
	if shortcut.Mode().Perm()&0o100 == 0 {
		t.Error("el lanzador del escritorio quedó sin el bit de ejecución")
	}
	body, err := os.ReadFile(result.Shortcut)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(body), "Exec=/usr/local/bin/dictador run") {
		t.Errorf("el Exec quedó mal:\n%s", body)
	}
	if _, err := os.Stat(result.Launcher); err != nil {
		t.Errorf("no quedó la entrada del menú: %v", err)
	}
	if _, err := os.Stat(IconPath()); err != nil {
		t.Errorf("no quedó el ícono: %v", err)
	}
	if result.Trusted {
		t.Error("KDE no necesita el marcado de confianza")
	}
}

func TestInstallMarcaConfiableDondeHaceFalta(t *testing.T) {
	home := fakeHome(t)
	t.Setenv("XDG_CURRENT_DESKTOP", "ubuntu:GNOME")
	if err := os.MkdirAll(filepath.Join(home, "Desktop"), 0o755); err != nil {
		t.Fatal(err)
	}
	var asked [][]string
	run = func(name string, args ...string) ([]byte, error) {
		asked = append(asked, append([]string{name}, args...))
		return nil, nil
	}
	// gio existe en cualquier máquina con GLib, incluida la que corre esto.
	if _, err := os.Stat("/usr/bin/gio"); err != nil {
		t.Skip("no hay gio en esta máquina")
	}

	result, err := Install("/usr/local/bin/dictador")
	if err != nil {
		t.Fatal(err)
	}
	if !result.Trusted {
		t.Fatalf("no lo marcó confiable: %+v", result)
	}
	var found bool
	for _, call := range asked {
		if call[0] == "gio" && strings.Contains(strings.Join(call, " "), "metadata::trusted") {
			found = true
		}
	}
	if !found {
		t.Fatalf("no le pidió a gio marcarlo: %v", asked)
	}
}

func TestInstallSobreEscribeYRecuperaElBitDeEjecucion(t *testing.T) {
	home := fakeHome(t)
	desk := filepath.Join(home, "Desktop")
	if err := os.MkdirAll(desk, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(desk, fileName), []byte("viejo"), 0o644); err != nil {
		t.Fatal(err)
	}
	result, err := Install("/usr/local/bin/dictador")
	if err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(result.Shortcut)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm()&0o100 == 0 {
		t.Fatal("reinstalar encima dejó el lanzador sin el bit de ejecución")
	}
}

func TestStatusYUninstall(t *testing.T) {
	home := fakeHome(t)
	if err := os.MkdirAll(filepath.Join(home, "Desktop"), 0o755); err != nil {
		t.Fatal(err)
	}
	if state := Status(); state.Installed {
		t.Fatal("todavía no hay nada instalado")
	}
	if _, err := Install("/usr/local/bin/dictador"); err != nil {
		t.Fatal(err)
	}
	state := Status()
	if !state.Installed || !state.DirExists {
		t.Fatalf("después de instalar dio %+v", state)
	}
	if _, err := Uninstall(); err != nil {
		t.Fatal(err)
	}
	if Status().Installed {
		t.Fatal("sigue instalado después de sacarlo")
	}
	if _, err := os.Stat(LauncherPath()); !os.IsNotExist(err) {
		t.Error("quedó la entrada del menú")
	}
	// Sacar dos veces no es un error: el que aprieta el botón de nuevo quiere
	// que no esté, y no está.
	if _, err := Uninstall(); err != nil {
		t.Fatalf("el segundo uninstall se quejó: %v", err)
	}
}

func TestInstallSeQuejaSinEscritorio(t *testing.T) {
	fakeHome(t)
	if _, err := Install("/usr/local/bin/dictador"); err == nil {
		t.Fatal("esperaba que se quejara de la carpeta que no existe")
	}
}

func TestAutostartSePoneYSeSaca(t *testing.T) {
	fakeHome(t)
	if AutostartInstalled() {
		t.Fatal("no tendría que arrancar solo todavía")
	}
	path, err := InstallAutostart("/usr/local/bin/dictador")
	if err != nil {
		t.Fatal(err)
	}
	if !AutostartInstalled() {
		t.Fatal("después de instalarlo tendría que arrancar solo")
	}
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(body), "X-GNOME-Autostart-enabled=true") {
		t.Errorf("el autostart no se declara como tal:\n%s", body)
	}
	if err := RemoveAutostart(); err != nil {
		t.Fatal(err)
	}
	if AutostartInstalled() {
		t.Fatal("sigue puesto después de sacarlo")
	}
	// Sacarlo dos veces es lo mismo que sacarlo una.
	if err := RemoveAutostart(); err != nil {
		t.Fatalf("el segundo se quejó: %v", err)
	}
}
