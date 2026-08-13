package cli

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/neitanod/dictador/internal/config"
)

func TestElCandadoDejaPasarUnoSolo(t *testing.T) {
	t.Setenv("XDG_RUNTIME_DIR", t.TempDir())

	first, _, err := takeLock()
	if err != nil {
		t.Fatalf("el primero no pudo tomarlo: %v", err)
	}
	defer first.release()

	_, other, err := takeLock()
	if err != errBusy {
		t.Fatalf("el segundo pasó igual: err=%v", err)
	}
	if other != os.Getpid() {
		t.Errorf("dijo que lo tiene el proceso %d, y lo tiene %d", other, os.Getpid())
	}
}

func TestElCandadoSeSueltaYElSiguienteEntra(t *testing.T) {
	t.Setenv("XDG_RUNTIME_DIR", t.TempDir())

	first, _, err := takeLock()
	if err != nil {
		t.Fatal(err)
	}
	first.release()

	second, _, err := takeLock()
	if err != nil {
		t.Fatalf("después de soltarlo el siguiente tendría que entrar: %v", err)
	}
	second.release()
}

func TestElCandadoDiceQuienLoTiene(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_RUNTIME_DIR", dir)

	held, _, err := takeLock()
	if err != nil {
		t.Fatal(err)
	}
	defer held.release()

	raw, err := os.ReadFile(filepath.Join(dir, "dictador.lock"))
	if err != nil {
		t.Fatal(err)
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(raw)))
	if err != nil {
		t.Fatalf("el candado no tiene un PID adentro: %q", raw)
	}
	if pid != os.Getpid() {
		t.Errorf("escribió el PID %d en vez de %d", pid, os.Getpid())
	}
}

// Sin XDG_RUNTIME_DIR —una sesión sin systemd, un contenedor pelado— el candado
// igual tiene que funcionar, y no puede ser el mismo archivo para dos usuarios
// de la misma máquina.
func TestElCandadoTieneLugarSinRuntimeDir(t *testing.T) {
	t.Setenv("XDG_RUNTIME_DIR", "")
	path := lockPath()
	if !strings.Contains(path, strconv.Itoa(os.Getuid())) {
		t.Errorf("el candado de /tmp tendría que llevar el uid: %q", path)
	}
}

func TestElAvisoDiceQueTeclaHayQueApretar(t *testing.T) {
	if got := hotkeyLabel(config.Config{Hotkey: config.Hotkey{Key: "AltGr+Control_R"}}); got != "AltGr+Control_R" {
		t.Errorf("dio %q, y tiene que decir la tecla del config", got)
	}
	// Sin tecla configurada igual tiene que decir algo que se entienda.
	if got := hotkeyLabel(config.Config{}); got == "" {
		t.Error("sin tecla en el config el aviso se quedó mudo")
	}
}

func TestRunNoArrancaDosVeces(t *testing.T) {
	t.Setenv("XDG_RUNTIME_DIR", t.TempDir())
	t.Setenv("HOME", t.TempDir())

	held, _, err := takeLock()
	if err != nil {
		t.Fatal(err)
	}
	defer held.release()

	// Con el candado tomado, `run` se tiene que ir solo y en paz: salir con
	// error haría que el escritorio lo trate como un lanzador roto.
	code, _, stderr := run(t, "run")
	if code != 0 {
		t.Errorf("exit = %d, quería 0", code)
	}
	if !strings.Contains(stderr, "Ya estaba andando") {
		t.Errorf("tendría que decir que ya hay uno: %q", stderr)
	}
	// Y decir qué tecla mantener, que es lo único accionable del mensaje.
	if !strings.Contains(stderr, "Mantené") || !strings.Contains(stderr, "Control") {
		t.Errorf("tendría que nombrar la tecla: %q", stderr)
	}
}
