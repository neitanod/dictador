package cli

import (
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/neitanod/dictador/internal/daemon"
)

func cmdRun(opts *options, args []string) int {
	fs := subflags("run", opts, opts.out.stderr)
	// --notify lo pone el .desktop del ícono, y no está pensado para escribirlo
	// a mano: cuando el que arranca el dictado es un doble click, la única
	// manera de saber que arrancó es que el escritorio lo diga.
	announce := fs.Bool("notify", false, "avisar por notificación del escritorio")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	opts.refresh()

	// La configuración se lee antes que nada porque de ahí sale la tecla, y la
	// tecla es lo que hay que decir en los dos caminos: el que arranca y el que
	// se encuentra con que ya había uno andando.
	cfg, err := opts.load()
	if err != nil {
		opts.out.fail(err, "CONFIG")
		return 1
	}
	hold := "Mantené " + hotkeyLabel(cfg) + " y hablá."

	// Y el candado antes de levantar nada: si ya hay uno, este proceso no tiene
	// nada que hacer más que decirlo.
	held, other, err := takeLock()
	if err == errBusy {
		message := "Ya estaba andando"
		if other > 0 {
			message = fmt.Sprintf("Ya estaba andando (proceso %d)", other)
		}
		fmt.Fprintf(opts.out.stderr, "%s. %s\n", message, hold)
		if *announce {
			notify("Dictador", message+". "+hold)
		}
		return 0
	}
	defer held.release()

	d, err := daemon.New(cfg, opts.verbose)
	if err != nil {
		opts.out.fail(err, "STARTUP")
		return 1
	}
	defer d.Stop()

	if !opts.quiet {
		fmt.Fprintf(opts.out.stdout, "dictador escuchando: %s modo %s → acción %s\n",
			d.Combo().Describe(), cfg.Hotkey.Mode, cfg.Action.OnRelease)
		fmt.Fprintf(opts.out.stdout, "motor de voz: %s\n", d.EngineLine())
		if url := d.ConfigURL(); url != "" {
			fmt.Fprintf(opts.out.stdout, "configuración: %s (o hacé click en la ventanita)\n", url)
		}
	}
	if *announce {
		if d.EngineFailed() {
			notify("Dictador sin motor de voz",
				"Arrancó, pero el motor no contesta: abrí la configuración y elegí otro.")
		} else {
			// La tecla va con el nombre que tiene en el config y en la página de
			// configuración, no con el que devuelve el mapa de X: el aviso está
			// para que sepas qué apretar, y "keycode 108, 105" no se aprieta.
			notify("Dictador andando",
				hold+" El texto entra donde estés escribiendo.")
		}
	}
	if d.EngineFailed() {
		// Sin motor no hay dictado posible: decirlo ahora, con la instrucción
		// para arreglarlo, en vez de esperar a que apriete la tecla y no pase
		// nada.
		fmt.Fprintf(opts.out.stderr,
			"elegí otro motor con `dictador config set stt.engine chrome` "+
				"(o google, con su API key)\n")
	}

	// Ctrl+C y SIGTERM cierran ordenado: Chrome, el clipboard y la conexión X.
	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM)
	done := make(chan error, 1)
	go func() { done <- d.Run() }()

	select {
	case sig := <-stop:
		opts.out.info("cortando por %s", sig)
		d.Stop()
		if sig == syscall.SIGTERM {
			return 143
		}
		return 130
	case err := <-done:
		if err != nil {
			opts.out.fail(err, "RUNTIME")
			return 1
		}
		return 0
	}
}
