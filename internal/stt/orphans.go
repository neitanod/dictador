package stt

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// Un dictador que se muere de mala manera —kill -9, un cuelgue, la sesión que
// se cierra— no llega a cerrar su Chrome. La página se da cuenta sola y se
// cierra a los 20s (ver chromePage), pero eso sólo alcanza para los Chrome que
// lanzó una versión con esa página: los que ya estaban dando vueltas, o uno que
// quedó colgado sin ejecutar javascript, siguen ahí para siempre. Así que cada
// dictador que arranca barre lo que dejaron los anteriores.
//
// Matar un proceso ajeno por error es mucho peor que dejar un huérfano vivo, y
// por eso para matar hacen falta dos certezas, no una:
//
//  1. Que el Chrome sea nuestro. Tiene que llevar --user-data-dir apuntando a un
//     directorio con el prefijo dictador-chrome- adentro del temporal, que es
//     una combinación que escribe solamente esta app. El Chrome de todos los
//     días apunta a ~/.config/google-chrome y ni entra en la lista.
//  2. Que el dueño esté muerto. Cada perfil lleva adentro el pid del dictador
//     que lo creó junto con su hora de arranque; ese par no se confunde aunque
//     el sistema recicle el número de pid. Si el dueño está vivo, su Chrome no
//     se toca, aunque sea de otra sesión.

// profilePrefix es el prefijo de los perfiles temporales, y la mitad de la
// firma que identifica a un Chrome nuestro.
const profilePrefix = "dictador-chrome-"

// ownerFile guarda quién creó el perfil, adentro del perfil mismo.
const ownerFile = "dictador.owner"

// browserNames: nombres de ejecutable que aceptamos como Chrome. Es un cinturón
// arriba de los tiradores — el --user-data-dir ya es exclusivo nuestro — para
// que un proceso cualquiera que mencione el directorio (un editor con el path
// abierto, un tar del perfil) no entre en la redada.
var browserNames = []string{"chrome", "chromium"}

// owner es el dictador que creó un perfil.
type owner struct {
	pid   int
	start string // hora de arranque del proceso, tal cual la cuenta el kernel
}

// chromeProc es un proceso de Chrome lanzado por algún dictador.
type chromeProc struct {
	pid     int
	ppid    int
	profile string
}

// SweepOrphanChromes mata los Chrome que dejaron dictadores muertos y borra sus
// perfiles. Se llama al arrancar, antes de lanzar el Chrome propio.
func SweepOrphanChromes(log func(string)) {
	sweepOrphans("/proc", os.TempDir(), killGroup, log)
}

// WriteOwner deja en el perfil quién lo creó. Se escribe antes de lanzar
// Chrome: entre el mkdir y el arranque hay una ventana en la que otro dictador
// podría estar barriendo, y un perfil sin dueño escrito parece de una versión
// vieja.
func WriteOwner(profile string) error { return writeOwner("/proc", profile, os.Getpid()) }

// sweepOrphans es SweepOrphanChromes con el /proc, el temporal y el matador
// afuera, para poder probarlo sin lanzar un Chrome ni matar nada de verdad.
func sweepOrphans(procRoot, tmpDir string, kill func([]int), log func(string)) (killed []int, removed []string) {
	procs := scanChromes(procRoot, tmpDir)

	// Un perfil se decide una sola vez, aunque tenga diez procesos colgando: el
	// browser, el zygote, los renderers y las utilities comparten el
	// --user-data-dir.
	porPerfil := map[string][]chromeProc{}
	for _, p := range procs {
		porPerfil[p.profile] = append(porPerfil[p.profile], p)
	}
	// Los perfiles sin ningún proceso vivo también se miran: son los que quedan
	// cuando el Chrome sí se cerró y nadie borró el directorio.
	for _, dir := range profileDirs(tmpDir) {
		if _, ok := porPerfil[dir]; !ok {
			porPerfil[dir] = nil
		}
	}

	for profile, grupo := range porPerfil {
		if ownerLives(procRoot, profile, grupo) {
			continue
		}
		var pids []int
		for _, p := range grupo {
			if p.pid == os.Getpid() {
				continue
			}
			pids = append(pids, p.pid)
		}
		if len(pids) > 0 {
			kill(pids)
			killed = append(killed, pids...)
		}
		if err := os.RemoveAll(profile); err == nil {
			removed = append(removed, profile)
		}
	}
	if log != nil && (len(killed) > 0 || len(removed) > 0) {
		log(fmt.Sprintf("Chrome huérfanos de dictadores anteriores: %d procesos matados, %d perfiles borrados",
			len(killed), len(removed)))
	}
	return killed, removed
}

// ownerLives dice si el dictador dueño del perfil sigue vivo.
func ownerLives(procRoot, profile string, grupo []chromeProc) bool {
	if own, ok := readOwner(profile); ok {
		return procLives(procRoot, own)
	}
	// Perfil de una versión anterior, que no dejó dueño escrito. El reemplazo es
	// mirar de quién cuelga el Chrome: mientras el dictador que lo lanzó esté
	// vivo, Chrome es hijo suyo y nunca cambia de padre. Recién cuando ese
	// dictador se muere el kernel lo reparenta al recolector de la sesión —init,
	// o el systemd de usuario—, y eso es lo único que damos por prueba de
	// orfandad. Cualquier otro padre lo dejamos en paz: preferimos que sobreviva
	// un huérfano a matarle el Chrome a un dictador que está trabajando.
	for _, p := range grupo {
		if !esRecolector(procRoot, p.ppid) {
			return true
		}
	}
	// Un perfil viejo sin ningún proceso vivo es basura de /tmp: no hay dueño
	// posible.
	return false
}

// esRecolector dice si ese pid es de los que heredan huérfanos.
func esRecolector(procRoot string, pid int) bool {
	if pid <= 1 {
		return true
	}
	comm, _, _, ok := statFields(procRoot, pid)
	if !ok {
		return true // el padre ya no está: más huérfano imposible
	}
	return comm == "systemd" || comm == "init"
}

// scanChromes recorre /proc y devuelve los Chrome lanzados por un dictador.
func scanChromes(procRoot, tmpDir string) []chromeProc {
	entries, err := os.ReadDir(procRoot)
	if err != nil {
		return nil
	}
	var found []chromeProc
	for _, entry := range entries {
		pid, err := strconv.Atoi(entry.Name())
		if err != nil {
			continue
		}
		args := cmdline(procRoot, pid)
		if len(args) == 0 || !isBrowser(args[0]) {
			continue
		}
		profile := dictadorProfile(args, tmpDir)
		if profile == "" {
			continue
		}
		_, ppid, _, ok := statFields(procRoot, pid)
		if !ok {
			continue
		}
		found = append(found, chromeProc{pid: pid, ppid: ppid, profile: profile})
	}
	return found
}

// isBrowser mira el nombre del ejecutable, que es lo que se ve en un ps.
func isBrowser(argv0 string) bool {
	name := strings.ToLower(filepath.Base(argv0))
	for _, want := range browserNames {
		if strings.Contains(name, want) {
			return true
		}
	}
	return false
}

// dictadorProfile devuelve el perfil si los argumentos traen un --user-data-dir
// que sólo pudo haber escrito esta app, y "" si no.
func dictadorProfile(args []string, tmpDir string) string {
	for _, arg := range args {
		dir, ok := strings.CutPrefix(arg, "--user-data-dir=")
		if !ok {
			continue
		}
		dir = filepath.Clean(dir)
		if filepath.Dir(dir) != filepath.Clean(tmpDir) {
			continue
		}
		if !strings.HasPrefix(filepath.Base(dir), profilePrefix) {
			continue
		}
		return dir
	}
	return ""
}

// profileDirs lista los perfiles que hay en el temporal, haya o no un Chrome
// usándolos.
func profileDirs(tmpDir string) []string {
	matches, err := filepath.Glob(filepath.Join(tmpDir, profilePrefix+"*"))
	if err != nil {
		return nil
	}
	var dirs []string
	for _, m := range matches {
		if info, err := os.Stat(m); err == nil && info.IsDir() {
			dirs = append(dirs, m)
		}
	}
	return dirs
}

func writeOwner(procRoot, profile string, pid int) error {
	_, _, start, ok := statFields(procRoot, pid)
	if !ok {
		return fmt.Errorf("no pude leer el arranque del proceso %d", pid)
	}
	line := fmt.Sprintf("%d %s\n", pid, start)
	return os.WriteFile(filepath.Join(profile, ownerFile), []byte(line), 0o600)
}

func readOwner(profile string) (owner, bool) {
	raw, err := os.ReadFile(filepath.Join(profile, ownerFile))
	if err != nil {
		return owner{}, false
	}
	parts := strings.Fields(string(raw))
	if len(parts) != 2 {
		return owner{}, false
	}
	pid, err := strconv.Atoi(parts[0])
	if err != nil {
		return owner{}, false
	}
	return owner{pid: pid, start: parts[1]}, true
}

// procLives dice si el proceso que anotó el perfil sigue siendo el mismo. El pid
// solo no alcanza: los números se reciclan, y matar al que heredó el número de
// un dictador muerto sería justo el error que este archivo trata de evitar.
func procLives(procRoot string, own owner) bool {
	_, _, start, ok := statFields(procRoot, own.pid)
	return ok && start == own.start
}

// cmdline lee los argumentos del proceso. En /proc vienen separados por NUL…
// salvo en Chrome, que reescribe su propia línea de comandos apenas arranca y
// la deja como un solo argumento con espacios adentro. Justamente el proceso
// que nos importa: sin este caso, el barrido no reconoce a ninguno.
//
// El corte por espacios se lleva puesto un path con espacios adentro —un
// TMPDIR raro—, y ahí el huérfano se salva de la quema. Es la falla que
// preferimos: la otra sería confundirlo con un pedazo de otro Chrome.
func cmdline(procRoot string, pid int) []string {
	raw, err := os.ReadFile(filepath.Join(procRoot, strconv.Itoa(pid), "cmdline"))
	if err != nil {
		return nil
	}
	args := strings.Split(strings.TrimRight(string(raw), "\x00"), "\x00")
	if len(args) == 1 {
		args = strings.Fields(args[0])
	}
	if len(args) == 0 {
		return nil
	}
	return args
}

// statFields saca el nombre, el padre y la hora de arranque de
// /proc/<pid>/stat. Se lee de ahí y no de /proc/<pid>/exe porque el stat lo
// puede leer cualquiera, y el exe de un proceso de root —init, por ejemplo— no.
// El nombre va entre paréntesis y puede tener espacios adentro, así que la
// cuenta de campos arranca después del último paréntesis.
func statFields(procRoot string, pid int) (comm string, ppid int, start string, ok bool) {
	raw, err := os.ReadFile(filepath.Join(procRoot, strconv.Itoa(pid), "stat"))
	if err != nil {
		return "", 0, "", false
	}
	line := string(raw)
	open, end := strings.Index(line, "("), strings.LastIndex(line, ")")
	if open < 0 || end < open {
		return "", 0, "", false
	}
	comm = line[open+1 : end]
	// fields[0] es el campo 3 del stat (el estado), así que el campo N está en
	// fields[N-3]: el padre es el 4 y la hora de arranque el 22.
	fields := strings.Fields(line[end+1:])
	if len(fields) < 20 {
		return "", 0, "", false
	}
	ppid, err = strconv.Atoi(fields[1])
	if err != nil {
		return "", 0, "", false
	}
	return comm, ppid, fields[19], true
}

// killGroup le pide al Chrome huérfano que se vaya y, si no se va, lo saca. Un
// huérfano no tiene nada que guardar, pero el TERM le deja soltar sus locks. Va
// de a grupos y no de a uno porque los hijos de Chrome —zygote, renderers,
// utilities— caen solos cuando cae el browser: esperarlos de a uno sería sumar
// dos segundos de arranque por proceso que ya está muerto.
func killGroup(pids []int) {
	for _, pid := range pids {
		_ = syscall.Kill(pid, syscall.SIGTERM)
	}
	for i := 0; i < 20; i++ {
		vivos := false
		for _, pid := range pids {
			if syscall.Kill(pid, 0) == nil {
				vivos = true
			}
		}
		if !vivos {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	for _, pid := range pids {
		_ = syscall.Kill(pid, syscall.SIGKILL)
	}
}
