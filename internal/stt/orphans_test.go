package stt

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

// Estos tests corren contra un /proc de mentira: son los que se pueden escribir
// sin lanzar un Chrome, y los que dicen a quién matamos y a quién no. Abajo del
// todo hay uno con un proceso de verdad, que es el que prueba que el /proc real
// se lee como creemos.

// procFalso es un proceso inventado adentro del /proc de mentira.
type procFalso struct {
	pid     int
	ppid    int
	exe     string
	cmdline []string
	// pegado escribe la línea de comandos como la deja Chrome: un solo argumento
	// con espacios adentro, en vez de los NUL de todo el resto del mundo.
	pegado bool
	start  string
}

// comm es el nombre corto del proceso, que es lo que el kernel pone en el stat.
func (p procFalso) comm() string {
	if p.exe == "" {
		return "chrome"
	}
	return filepath.Base(p.exe)
}

func armarProc(t *testing.T, root string, p procFalso) {
	t.Helper()
	dir := filepath.Join(root, strconv.Itoa(p.pid))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if p.cmdline != nil {
		linea := strings.Join(p.cmdline, "\x00") + "\x00"
		if p.pegado {
			linea = strings.Join(p.cmdline, " ") + "\x00"
		}
		if err := os.WriteFile(filepath.Join(dir, "cmdline"), []byte(linea), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	start := p.start
	if start == "" {
		start = "1000"
	}
	// El campo 22 es la hora de arranque: pid, (comm), estado, padre y después
	// diecisiete campos que acá no interesan.
	stat := fmt.Sprintf("%d (%s) S %d %s%s\n", p.pid, p.comm(), p.ppid, strings.Repeat("0 ", 17), start)
	if err := os.WriteFile(filepath.Join(dir, "stat"), []byte(stat), 0o644); err != nil {
		t.Fatal(err)
	}
	if p.exe != "" {
		if err := os.Symlink(p.exe, filepath.Join(dir, "exe")); err != nil {
			t.Fatal(err)
		}
	}
}

// armarPerfil crea un perfil en el temporal de mentira. Con dueño, si se le pasa
// uno.
func armarPerfil(t *testing.T, tmpDir, nombre string, dueño string) string {
	t.Helper()
	dir := filepath.Join(tmpDir, nombre)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if dueño != "" {
		if err := os.WriteFile(filepath.Join(dir, ownerFile), []byte(dueño), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

// armarRecolectores pone en el /proc de mentira los procesos que heredan
// huérfanos: init y el systemd de usuario.
func armarRecolectores(t *testing.T, procRoot string) {
	t.Helper()
	armarProc(t, procRoot, procFalso{pid: 1, ppid: 0, exe: "/usr/lib/systemd/systemd"})
	armarProc(t, procRoot, procFalso{pid: 2000, ppid: 1, exe: "/usr/lib/systemd/systemd"})
}

func chromeArgs(profile string) []string {
	return []string{"/opt/google/chrome/chrome", "--headless=new", "--user-data-dir=" + profile, "http://127.0.0.1:1234/"}
}

// El caso que motivó todo: un dictador que se murió sin cerrar su Chrome. El
// dueño anotado en el perfil ya no está vivo, así que el Chrome se mata y el
// perfil se borra.
func TestMataAlChromeDeUnDictadorMuerto(t *testing.T) {
	procRoot, tmpDir := t.TempDir(), t.TempDir()
	armarRecolectores(t, procRoot)
	// El dueño era el pid 900, que ya no figura en /proc.
	profile := armarPerfil(t, tmpDir, profilePrefix+"muerto", "900 555\n")
	armarProc(t, procRoot, procFalso{pid: 1001, ppid: 1, exe: "/opt/google/chrome/chrome", cmdline: chromeArgs(profile)})

	killed, removed := sweepOrphans(procRoot, tmpDir, func([]int) {}, nil)

	if len(killed) != 1 || killed[0] != 1001 {
		t.Errorf("tendría que haber matado al 1001, mató %v", killed)
	}
	if len(removed) != 1 || removed[0] != profile {
		t.Errorf("tendría que haber borrado %s, borró %v", profile, removed)
	}
	if _, err := os.Stat(profile); !os.IsNotExist(err) {
		t.Errorf("el perfil huérfano quedó en %s", profile)
	}
}

// Chrome no deja su línea de comandos como la dejan los demás: la reescribe
// apenas arranca y en /proc queda un solo argumento con espacios adentro, sin
// los NUL que separan. Es exactamente la forma que tiene el proceso que
// buscamos, así que si el barrido no la entiende no encuentra a nadie.
func TestEntiendeLaLineaDeComandosPegadaDeChrome(t *testing.T) {
	procRoot, tmpDir := t.TempDir(), t.TempDir()
	armarRecolectores(t, procRoot)
	profile := armarPerfil(t, tmpDir, profilePrefix+"pegado", "900 555\n")
	// Copiada de un Chrome de verdad lanzado por el dictador.
	real := []string{
		"/opt/google/chrome/chrome", "--headless=new", "--disable-gpu", "--no-first-run",
		"--no-default-browser-check", "--disable-extensions", "--user-data-dir=" + profile,
		"--use-fake-ui-for-media-stream", "--noerrdialogs", "--ozone-platform=headless",
		"--ozone-override-screen-size=800,600", "--use-angle=swiftshader-webgl",
		"http://127.0.0.1:37087/",
	}
	armarProc(t, procRoot, procFalso{pid: 6001, ppid: 1, exe: "/opt/google/chrome/chrome", cmdline: real, pegado: true})

	killed, _ := sweepOrphans(procRoot, tmpDir, func([]int) {}, nil)

	if len(killed) != 1 || killed[0] != 6001 {
		t.Errorf("tendría que haber matado al 6001, mató %v", killed)
	}
}

// La otra mitad del trato: si el dictador dueño sigue vivo, su Chrome no se
// toca. Pasa cada vez que hay dos dictadores corriendo a la vez.
func TestNoTocaElChromeDeUnDictadorVivo(t *testing.T) {
	procRoot, tmpDir := t.TempDir(), t.TempDir()
	armarRecolectores(t, procRoot)
	profile := armarPerfil(t, tmpDir, profilePrefix+"vivo", "900 555\n")
	armarProc(t, procRoot, procFalso{pid: 900, ppid: 1, exe: "/usr/local/bin/dictador", start: "555"})
	armarProc(t, procRoot, procFalso{pid: 1001, ppid: 900, exe: "/opt/google/chrome/chrome", cmdline: chromeArgs(profile)})

	killed, removed := sweepOrphans(procRoot, tmpDir, func([]int) {}, nil)

	if len(killed) != 0 || len(removed) != 0 {
		t.Errorf("tocó lo del dictador vivo: mató %v, borró %v", killed, removed)
	}
}

// El número de pid se recicla. Un proceso cualquiera que heredó el número del
// dictador muerto no lo convierte en vivo: por eso el perfil anota también la
// hora de arranque.
func TestElPidRecicladoNoSalvaAlHuerfano(t *testing.T) {
	procRoot, tmpDir := t.TempDir(), t.TempDir()
	armarRecolectores(t, procRoot)
	profile := armarPerfil(t, tmpDir, profilePrefix+"reciclado", "900 555\n")
	// Mismo pid, otro arranque: es otro proceso.
	armarProc(t, procRoot, procFalso{pid: 900, ppid: 1, exe: "/usr/bin/firefox", start: "9999"})
	armarProc(t, procRoot, procFalso{pid: 1001, ppid: 900, exe: "/opt/google/chrome/chrome", cmdline: chromeArgs(profile)})

	killed, _ := sweepOrphans(procRoot, tmpDir, func([]int) {}, nil)

	if len(killed) != 1 || killed[0] != 1001 {
		t.Errorf("el huérfano tendría que haber caído igual, mató %v", killed)
	}
}

// Lo que nunca puede pasar: que el barrido toque el Chrome de todos los días.
func TestNoMiraElChromeDelUsuario(t *testing.T) {
	procRoot, tmpDir := t.TempDir(), t.TempDir()
	armarRecolectores(t, procRoot)
	casa := t.TempDir()
	ajenos := [][]string{
		{"/opt/google/chrome/chrome", "--user-data-dir=" + filepath.Join(casa, ".config", "google-chrome")},
		{"/opt/google/chrome/chrome"}, // el Chrome sin --user-data-dir, el más común
		{"/usr/bin/firefox", "--user-data-dir=" + filepath.Join(tmpDir, profilePrefix+"noesnuestro")},
		// Un perfil con nuestro nombre pero fuera del temporal no es nuestro.
		{"/opt/google/chrome/chrome", "--user-data-dir=" + filepath.Join(casa, profilePrefix+"falso")},
		// Y uno adentro de un subdirectorio del temporal tampoco.
		{"/opt/google/chrome/chrome", "--user-data-dir=" + filepath.Join(tmpDir, "hondo", profilePrefix+"falso")},
	}
	for i, args := range ajenos {
		armarProc(t, procRoot, procFalso{pid: 5000 + i, ppid: 1, exe: args[0], cmdline: args})
	}

	killed, removed := sweepOrphans(procRoot, tmpDir, func([]int) {}, nil)

	if len(killed) != 0 {
		t.Errorf("se metió con procesos ajenos: %v", killed)
	}
	if len(removed) != 0 {
		t.Errorf("borró directorios ajenos: %v", removed)
	}
}

// Los perfiles que dejó la versión anterior no tienen dueño escrito. Ahí el
// criterio es de quién cuelga el proceso: reparentado a init, es huérfano.
func TestPerfilViejoSinDuenoSeDecidePorElPadre(t *testing.T) {
	procRoot, tmpDir := t.TempDir(), t.TempDir()
	armarRecolectores(t, procRoot)

	// Reparentado a init: huérfano seguro.
	initHuerfano := armarPerfil(t, tmpDir, profilePrefix+"viejoinit", "")
	armarProc(t, procRoot, procFalso{pid: 3001, ppid: 1, exe: "/opt/google/chrome/chrome", cmdline: chromeArgs(initHuerfano)})

	// Reparentado al systemd de usuario, que es a dónde caen los huérfanos de una
	// sesión de escritorio: también.
	userHuerfano := armarPerfil(t, tmpDir, profilePrefix+"viejouser", "")
	armarProc(t, procRoot, procFalso{pid: 3002, ppid: 2000, exe: "/opt/google/chrome/chrome", cmdline: chromeArgs(userHuerfano)})

	// Colgando de un dictador: se queda.
	colgando := armarPerfil(t, tmpDir, profilePrefix+"viejovivo", "")
	armarProc(t, procRoot, procFalso{pid: 3100, ppid: 1, exe: "/usr/local/bin/dictador"})
	armarProc(t, procRoot, procFalso{pid: 3101, ppid: 3100, exe: "/opt/google/chrome/chrome", cmdline: chromeArgs(colgando)})

	// Y colgando de cualquier otra cosa —una suite de tests, un script, un
	// dictador recompilado que ya no coincide con ningún path conocido— también
	// se queda: si alguien lo tiene tomado, no es huérfano.
	dudoso := armarPerfil(t, tmpDir, profilePrefix+"viejodudoso", "")
	armarProc(t, procRoot, procFalso{pid: 3200, ppid: 1, exe: "/tmp/go-build123/stt.test"})
	armarProc(t, procRoot, procFalso{pid: 3201, ppid: 3200, exe: "/opt/google/chrome/chrome", cmdline: chromeArgs(dudoso)})

	killed, removed := sweepOrphans(procRoot, tmpDir, func([]int) {}, nil)

	if len(killed) != 2 || !contiene(killed, 3001) || !contiene(killed, 3002) {
		t.Errorf("tendría que haber matado sólo a los reparentados 3001 y 3002, mató %v", killed)
	}
	if len(removed) != 2 || !contieneStr(removed, initHuerfano) || !contieneStr(removed, userHuerfano) {
		t.Errorf("tendría que haber borrado sólo los dos huérfanos, borró %v", removed)
	}
}

func contiene(lista []int, quiero int) bool {
	for _, v := range lista {
		if v == quiero {
			return true
		}
	}
	return false
}

func contieneStr(lista []string, quiero string) bool {
	for _, v := range lista {
		if v == quiero {
			return true
		}
	}
	return false
}

// Un perfil sin ningún Chrome usándolo es lo que queda cuando el Chrome sí se
// cerró: unos megas en /tmp que nadie borra. Con dueño muerto se van; con dueño
// vivo se quedan, porque ese dictador está por lanzar su Chrome ahí.
func TestBorraLosPerfilesSinDuenoVivoYDejaLosDelDictadorVivo(t *testing.T) {
	procRoot, tmpDir := t.TempDir(), t.TempDir()
	armarRecolectores(t, procRoot)
	muerto := armarPerfil(t, tmpDir, profilePrefix+"sobrante", "900 555\n")
	vivo := armarPerfil(t, tmpDir, profilePrefix+"recien", "901 777\n")
	armarProc(t, procRoot, procFalso{pid: 901, ppid: 1, exe: "/usr/local/bin/dictador", start: "777"})

	killed, _ := sweepOrphans(procRoot, tmpDir, func([]int) {}, nil)

	if len(killed) != 0 {
		t.Errorf("no había procesos para matar, mató %v", killed)
	}
	if _, err := os.Stat(muerto); !os.IsNotExist(err) {
		t.Errorf("el perfil sin dueño vivo quedó en %s", muerto)
	}
	if _, err := os.Stat(vivo); err != nil {
		t.Errorf("borró el perfil del dictador vivo: %v", err)
	}
}

// Todos los procesos del mismo perfil caen juntos: el browser, el zygote y los
// renderers comparten el --user-data-dir.
func TestMataATodoElArbolDelPerfilHuerfano(t *testing.T) {
	procRoot, tmpDir := t.TempDir(), t.TempDir()
	armarRecolectores(t, procRoot)
	profile := armarPerfil(t, tmpDir, profilePrefix+"arbol", "900 555\n")
	armarProc(t, procRoot, procFalso{pid: 4001, ppid: 1, exe: "/opt/google/chrome/chrome", cmdline: chromeArgs(profile)})
	armarProc(t, procRoot, procFalso{pid: 4002, ppid: 4001, exe: "/opt/google/chrome/chrome",
		cmdline: []string{"/opt/google/chrome/chrome", "--type=renderer", "--user-data-dir=" + profile}})
	armarProc(t, procRoot, procFalso{pid: 4003, ppid: 4001, exe: "/opt/google/chrome/chrome",
		cmdline: []string{"/opt/google/chrome/chrome", "--type=zygote", "--user-data-dir=" + profile}})

	killed, _ := sweepOrphans(procRoot, tmpDir, func([]int) {}, nil)

	if len(killed) != 3 {
		t.Errorf("tendría que haber matado a los 3 del árbol, mató %v", killed)
	}
}

// El dueño se escribe leyendo el /proc de verdad, y se relee igual.
func TestElDuenoEscritoSeReconoceVivo(t *testing.T) {
	profile := t.TempDir()
	if err := WriteOwner(profile); err != nil {
		t.Fatalf("no pude escribir el dueño: %v", err)
	}
	own, ok := readOwner(profile)
	if !ok {
		t.Fatal("el dueño recién escrito no se pudo leer")
	}
	if own.pid != os.Getpid() {
		t.Errorf("el dueño quedó como %d y este proceso es %d", own.pid, os.Getpid())
	}
	if !procLives("/proc", own) {
		t.Error("este proceso está vivo y el dueño escrito dice que no")
	}
	if procLives("/proc", owner{pid: own.pid, start: "1"}) {
		t.Error("otra hora de arranque tendría que dar por muerto al dueño")
	}
}

// Y el que cierra el círculo: un proceso de verdad, con un --user-data-dir de
// verdad, leído del /proc de verdad y matado de verdad. Es lo único que prueba
// que el parseo de /proc y el kill hacen lo que decimos.
func TestMataUnProcesoDeVerdad(t *testing.T) {
	tmpDir := t.TempDir()
	profile := armarPerfil(t, tmpDir, profilePrefix+"real", "900 555\n") // dueño inexistente

	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	// Se llama "chrome" y lleva el --user-data-dir del perfil huérfano: para el
	// barrido es indistinguible de un Chrome nuestro abandonado.
	cmd := &exec.Cmd{
		Path: exe,
		Args: []string{"chrome", "-test.run=TestAyudanteQueDuerme", "--", "--user-data-dir=" + profile},
		Env:  append(os.Environ(), "DICTADOR_AYUDANTE=1"),
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	muerto := make(chan struct{})
	go func() { _ = cmd.Wait(); close(muerto) }()
	defer func() { _ = cmd.Process.Kill() }()

	// Esperar a que el kernel publique la cmdline del hijo.
	esperar(t, func() bool {
		for _, p := range scanChromes("/proc", tmpDir) {
			if p.pid == cmd.Process.Pid {
				return true
			}
		}
		return false
	}, "el proceso no apareció en el barrido")

	killed, removed := sweepOrphans("/proc", tmpDir, killGroup, nil)

	if len(killed) != 1 || killed[0] != cmd.Process.Pid {
		t.Fatalf("tendría que haber matado al %d, mató %v", cmd.Process.Pid, killed)
	}
	select {
	case <-muerto:
	case <-time.After(10 * time.Second):
		t.Error("el proceso siguió vivo después del barrido")
	}
	if len(removed) != 1 {
		t.Errorf("tendría que haber borrado el perfil, borró %v", removed)
	}
}

// Y el mismo proceso, con el dueño vivo, no se toca.
func TestNoMataUnProcesoDeVerdadConDuenoVivo(t *testing.T) {
	tmpDir := t.TempDir()
	profile := filepath.Join(tmpDir, profilePrefix+"realvivo")
	if err := os.MkdirAll(profile, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := WriteOwner(profile); err != nil { // el dueño es este test, que está vivo
		t.Fatal(err)
	}

	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	cmd := &exec.Cmd{
		Path: exe,
		Args: []string{"chrome", "-test.run=TestAyudanteQueDuerme", "--", "--user-data-dir=" + profile},
		Env:  append(os.Environ(), "DICTADOR_AYUDANTE=1"),
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = cmd.Process.Kill(); _ = cmd.Wait() }()

	esperar(t, func() bool {
		for _, p := range scanChromes("/proc", tmpDir) {
			if p.pid == cmd.Process.Pid {
				return true
			}
		}
		return false
	}, "el proceso no apareció en el barrido")

	killed, removed := sweepOrphans("/proc", tmpDir, killGroup, nil)

	if len(killed) != 0 || len(removed) != 0 {
		t.Fatalf("mató lo de un dueño vivo: %v %v", killed, removed)
	}
	if syscall.Kill(cmd.Process.Pid, 0) != nil {
		t.Error("el proceso del dueño vivo terminó muerto")
	}
}

func esperar(t *testing.T, listo func() bool, queja string) {
	t.Helper()
	for i := 0; i < 100; i++ {
		if listo() {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatal(queja)
}

// TestAyudanteQueDuerme no es un test: es el proceso que lanzan los dos de acá
// arriba para tener algo real a lo que apuntarle.
func TestAyudanteQueDuerme(t *testing.T) {
	if os.Getenv("DICTADOR_AYUDANTE") != "1" {
		t.Skip("es el proceso ayudante de otro test")
	}
	time.Sleep(60 * time.Second)
}
