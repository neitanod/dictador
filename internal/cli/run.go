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
	if err := fs.Parse(args); err != nil {
		return 2
	}
	opts.refresh()

	cfg, err := opts.load()
	if err != nil {
		opts.out.fail(err, "CONFIG")
		return 1
	}

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
