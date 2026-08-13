package cli

import (
	"errors"
	"fmt"
	"syscall"
	"time"
)

// Parar el dictador que está andando.
//
// Hace falta por lo mismo que hace falta el candado: el programa no muestra
// ninguna ventana, así que no hay una cruz para cerrarlo. Desde la terminal que
// lo arrancó está el Ctrl+C, y esa terminal es justo la que no existe cuando lo
// prendiste con un doble click en el ícono.
//
// A quién matar lo contesta el candado, y por eso este comando no anda buscando
// procesos por nombre en /proc: el que tiene el flock es el dictador que está
// escuchando la tecla, sin ambigüedad y sin riesgo de llevarse puesto un proceso
// ajeno que se llame parecido.

// killer es syscall.Kill con un nombre propio, para que los tests puedan
// ponerse en el lugar del que recibe la señal.
var killer = syscall.Kill

// Cuánto se espera en cada etapa. El TERM le da tiempo a cerrar Chrome, soltar
// el clipboard y despedirse de X —lo que hace d.Stop()—, que en una máquina
// cargada puede tardar bastante más de lo que uno cree. Después del KILL la
// espera es corta: es sólo para confirmar que el kernel lo bajó.
var (
	stopGrace = 5 * time.Second
	stopHard  = 2 * time.Second
)

// stopOutcome es lo que pasó, para el humano y para el JSON.
type stopOutcome struct {
	Running bool `json:"running"` // ¿había alguno andando?
	PID     int  `json:"pid,omitempty"`
	Forced  bool `json:"forced"` // hubo que sacarlo con SIGKILL
}

func cmdShutdown(opts *options, args []string) int {
	fs := subflags("shutdown", opts, opts.out.stderr)
	if err := fs.Parse(args); err != nil {
		return 2
	}
	opts.refresh()

	result, err := stopRunning()
	if err != nil {
		opts.out.fail(err, "SHUTDOWN")
		return 1
	}

	var lines []string
	switch {
	case !result.Running:
		lines = []string{"no había ninguno andando"}
	case result.Forced:
		lines = []string{
			fmt.Sprintf("el proceso %d no se iba por las buenas: lo saqué con SIGKILL", result.PID),
			"si dejó un Chrome dando vueltas, el próximo arranque lo barre",
		}
	default:
		lines = []string{fmt.Sprintf("listo: paré el dictador (proceso %d)", result.PID)}
	}
	_ = opts.out.print(result, lines)
	return 0
}

// stopRunning le pide al dueño del candado que se vaya, y se asegura de que se
// haya ido.
//
// La prueba de que murió es que el candado quedó libre, y no que `kill -0` deje
// de encontrarlo: el flock lo suelta el kernel recién cuando el proceso termina
// de verdad, y no se deja engañar por un PID reciclado en el medio. De paso es
// exactamente la condición que le importa al que va a arrancar el siguiente.
func stopRunning() (stopOutcome, error) {
	pid, busy, err := lockOwner()
	if err != nil {
		return stopOutcome{}, err
	}
	if !busy {
		return stopOutcome{}, nil
	}
	if pid <= 0 {
		return stopOutcome{Running: true}, fmt.Errorf(
			"hay un dictador andando y el candado %s no dice cuál: paralo a mano con `pkill dictador`", lockPath())
	}

	result := stopOutcome{Running: true, PID: pid}
	if err := send(pid, syscall.SIGTERM); err != nil {
		return result, err
	}
	if waitFree(stopGrace) {
		return result, nil
	}

	result.Forced = true
	if err := send(pid, syscall.SIGKILL); err != nil {
		return result, err
	}
	if waitFree(stopHard) {
		return result, nil
	}
	return result, fmt.Errorf("el proceso %d sigue con el candado tomado después del SIGKILL", pid)
}

// send manda la señal y perdona al que ya no está: entre que leímos el candado y
// llegamos acá el dictador pudo haberse cerrado solo, y eso es que salió bien.
func send(pid int, sig syscall.Signal) error {
	if err := killer(pid, sig); err != nil && !errors.Is(err, syscall.ESRCH) {
		return fmt.Errorf("no pude mandarle %v al proceso %d: %w", sig, pid, err)
	}
	return nil
}

// waitFree espera a que el candado quede libre, y dice si llegó a estarlo.
func waitFree(within time.Duration) bool {
	deadline := time.Now().Add(within)
	for {
		if _, busy, err := lockOwner(); err == nil && !busy {
			return true
		}
		if time.Now().After(deadline) {
			return false
		}
		time.Sleep(25 * time.Millisecond)
	}
}
