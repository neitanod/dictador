package cli

import (
	"fmt"
	"os"

	"github.com/neitanod/dictador/internal/desktop"
)

// El autostart va por el .desktop de XDG, que es lo que KDE, GNOME y Xfce leen
// igual. Una unit de systemd --user también andaría, y traería una dependencia
// del orden de arranque de la sesión gráfica que este .desktop no tiene.
//
// Es el mismo archivo que el ícono del escritorio, con una línea de más: los
// dos los arma internal/desktop, así que arreglar algo del lanzador lo arregla
// en los dos lugares.
func cmdService(opts *options, args []string) int {
	fs := subflags("service", opts, opts.out.stderr)
	if err := fs.Parse(args); err != nil {
		return 2
	}
	opts.refresh()
	action := firstArg(fs.Args(), "status")
	path := desktop.AutostartPath()

	switch action {
	case "install":
		if _, err := desktop.InstallAutostart(desktop.Binary()); err != nil {
			opts.out.fail(err, "SERVICE")
			return 1
		}
		_ = opts.out.print(map[string]any{"installed": true, "path": path}, []string{
			"autostart instalado en " + path,
			"Arranca solo en el próximo login. Para probarlo ahora: dictador run",
		})
		return 0

	case "uninstall":
		if _, err := os.Stat(path); os.IsNotExist(err) {
			_ = opts.out.print(map[string]any{"installed": false}, []string{"no estaba instalado"})
			return 0
		}
		if err := desktop.RemoveAutostart(); err != nil {
			opts.out.fail(err, "SERVICE")
			return 1
		}
		_ = opts.out.print(map[string]any{"installed": false, "path": path},
			[]string{"borré " + path})
		return 0

	case "status":
		installed := desktop.AutostartInstalled()
		_ = opts.out.print(map[string]any{"installed": installed, "path": path},
			[]string{ifElse(installed, "instalado", "no instalado")})
		return 0

	default:
		fmt.Fprintf(opts.out.stderr,
			"dictador service: no conozco %q (install | uninstall | status)\n", action)
		return 2
	}
}
