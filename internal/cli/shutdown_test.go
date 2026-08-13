package cli

import (
	"encoding/json"
	"os"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"
)

// matador simula al que recibe las señales: anota lo que le llega y, cuando le
// toca, suelta el candado como haría un proceso al morirse.
type matador struct {
	mu      sync.Mutex
	señales []syscall.Signal
	pids    []int
	muere   syscall.Signal // con cuál se va; 0 = no se va con ninguna
	candado *lock
}

func (m *matador) send(pid int, sig syscall.Signal) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.pids = append(m.pids, pid)
	m.señales = append(m.señales, sig)
	if m.muere != 0 && sig == m.muere {
		m.candado.release()
	}
	return nil
}

func (m *matador) recibidas() []syscall.Signal {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]syscall.Signal(nil), m.señales...)
}

// plazosCortos deja los tiempos de espera en algo que no alargue los tests.
func plazosCortos(t *testing.T) {
	t.Helper()
	graceAntes, hardAntes := stopGrace, stopHard
	stopGrace, stopHard = 200*time.Millisecond, 200*time.Millisecond
	t.Cleanup(func() { stopGrace, stopHard = graceAntes, hardAntes })
}

// conCandadoTomado deja el candado puesto, como si hubiera un dictador andando,
// y devuelve el matador que va a hacer de proceso dueño.
func conCandadoTomado(t *testing.T, muere syscall.Signal) *matador {
	t.Helper()
	t.Setenv("XDG_RUNTIME_DIR", t.TempDir())
	plazosCortos(t)

	held, _, err := takeLock()
	if err != nil {
		t.Fatalf("no pude preparar el candado: %v", err)
	}
	t.Cleanup(held.release)

	m := &matador{muere: muere, candado: held}
	antes := killer
	killer = m.send
	t.Cleanup(func() { killer = antes })
	return m
}

func TestShutdownSinNadieAndandoSaleEnPaz(t *testing.T) {
	t.Setenv("XDG_RUNTIME_DIR", t.TempDir())
	plazosCortos(t)
	m := &matador{}
	antes := killer
	killer = m.send
	t.Cleanup(func() { killer = antes })

	code, stdout, _ := run(t, "shutdown")
	if code != 0 {
		t.Errorf("exit = %d, quería 0: parar lo que no está andando no es un error", code)
	}
	if !strings.Contains(stdout, "no había ninguno andando") {
		t.Errorf("stdout = %q", stdout)
	}
	if len(m.recibidas()) != 0 {
		t.Errorf("no tenía a quién mandarle señales y mandó %v", m.recibidas())
	}
}

func TestShutdownLePideAlQueCorreQueSeVaya(t *testing.T) {
	m := conCandadoTomado(t, syscall.SIGTERM)

	code, stdout, stderr := run(t, "shutdown")
	if code != 0 {
		t.Fatalf("exit = %d: %s", code, stderr)
	}
	if got := m.recibidas(); len(got) != 1 || got[0] != syscall.SIGTERM {
		t.Errorf("le mandó %v, quería un SIGTERM solo", got)
	}
	if m.pids[0] != os.Getpid() {
		t.Errorf("le mandó la señal al proceso %d en vez de al dueño del candado %d", m.pids[0], os.Getpid())
	}
	if !strings.Contains(stdout, "paré el dictador") {
		t.Errorf("stdout = %q", stdout)
	}
}

func TestShutdownSacaAlQueNoSeVaPorLasBuenas(t *testing.T) {
	m := conCandadoTomado(t, syscall.SIGKILL)

	code, stdout, stderr := run(t, "shutdown")
	if code != 0 {
		t.Fatalf("exit = %d: %s", code, stderr)
	}
	got := m.recibidas()
	if len(got) != 2 || got[0] != syscall.SIGTERM || got[1] != syscall.SIGKILL {
		t.Errorf("mandó %v, quería SIGTERM y después SIGKILL", got)
	}
	if !strings.Contains(stdout, "no se iba") {
		t.Errorf("tendría que contar que hubo que forzarlo: %q", stdout)
	}
}

func TestShutdownFallaSiElProcesoSobreviveAlKill(t *testing.T) {
	m := conCandadoTomado(t, 0) // no se muere con nada

	code, _, stderr := run(t, "shutdown")
	if code != 1 {
		t.Errorf("exit = %d, quería 1: no logró pararlo", code)
	}
	if !strings.Contains(stderr, "sigue") {
		t.Errorf("stderr = %q", stderr)
	}
	if len(m.recibidas()) != 2 {
		t.Errorf("tendría que haber probado las dos señales: %v", m.recibidas())
	}
}

func TestShutdownSaleEnJSON(t *testing.T) {
	conCandadoTomado(t, syscall.SIGTERM)

	code, stdout, stderr := run(t, "--json", "shutdown")
	if code != 0 {
		t.Fatalf("exit = %d: %s", code, stderr)
	}
	var parsed struct {
		Running bool `json:"running"`
		PID     int  `json:"pid"`
		Forced  bool `json:"forced"`
	}
	if err := json.Unmarshal([]byte(stdout), &parsed); err != nil {
		t.Fatalf("no es JSON: %v\n%s", err, stdout)
	}
	if !parsed.Running || parsed.PID != os.Getpid() || parsed.Forced {
		t.Errorf("el JSON no cuenta lo que pasó: %+v", parsed)
	}
}

// El candado libre no se toca: probarlo no puede dejar adentro el PID del que
// preguntó, o el próximo `run` que se choque con otro dictador nombraría a un
// proceso que ya no existe.
func TestPreguntarPorElCandadoNoLoEnsucia(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_RUNTIME_DIR", dir)

	held, _, err := takeLock()
	if err != nil {
		t.Fatal(err)
	}
	held.release()

	pid, busy, err := lockOwner()
	if err != nil || busy || pid != 0 {
		t.Fatalf("el candado libre dio pid=%d busy=%v err=%v", pid, busy, err)
	}
	raw, err := os.ReadFile(lockPath())
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(string(raw)) != strconv.Itoa(os.Getpid()) {
		// El único PID que puede quedar es el que escribió takeLock más arriba.
		t.Errorf("el archivo del candado dice %q y nadie lo tomó desde entonces", raw)
	}
}

func TestLockOwnerDiceQuienLoTiene(t *testing.T) {
	t.Setenv("XDG_RUNTIME_DIR", t.TempDir())

	held, _, err := takeLock()
	if err != nil {
		t.Fatal(err)
	}
	defer held.release()

	pid, busy, err := lockOwner()
	if err != nil {
		t.Fatal(err)
	}
	if !busy {
		t.Fatal("dijo que estaba libre y lo tenemos tomado")
	}
	if pid != os.Getpid() {
		t.Errorf("dijo que lo tiene %d y lo tiene %d", pid, os.Getpid())
	}
}
