package cli

import (
	"fmt"

	"github.com/neitanod/dictador/internal/desktop"
)

// cmdDesktop pone el ícono en el escritorio y la entrada en el menú.
//
// Es lo mismo que hace el botón de la página de configuración, y existe acá
// porque el que todavía no arrancó el dictador no tiene página que abrir: el
// primer ícono de todos se pone desde la terminal, y del segundo en adelante ya
// se puede desde la ventana.
func cmdDesktop(opts *options, args []string) int {
	fs := subflags("desktop", opts, opts.out.stderr)
	if err := fs.Parse(args); err != nil {
		return 2
	}
	opts.refresh()
	action := firstArg(fs.Args(), "status")

	switch action {
	case "install":
		result, err := desktop.Install(desktop.Binary())
		if err != nil {
			opts.out.fail(err, "DESKTOP")
			return 1
		}
		lines := []string{
			"ícono puesto en " + result.Shortcut,
			"y en el menú, en " + result.Launcher,
		}
		if result.Desktop != "" {
			lines = append(lines, "escritorio detectado: "+result.Desktop+
				ifElse(result.Trusted, ", marcado como lanzador confiable", ""))
		}
		lines = append(lines, result.Notes...)
		_ = opts.out.print(result, lines)
		return 0

	case "uninstall":
		result, err := desktop.Uninstall()
		if err != nil {
			opts.out.fail(err, "DESKTOP")
			return 1
		}
		_ = opts.out.print(map[string]any{"installed": false, "shortcut": result.Shortcut},
			[]string{"saqué el ícono del escritorio y la entrada del menú"})
		return 0

	case "status":
		state := desktop.Status()
		lines := []string{ifElse(state.Installed,
			"el ícono está en "+state.Shortcut,
			"no hay ícono en el escritorio")}
		if state.Desktop != "" {
			lines = append(lines, "escritorio detectado: "+state.Desktop)
		}
		if !state.DirExists {
			lines = append(lines, "no encontré la carpeta de tu escritorio")
		}
		_ = opts.out.print(state, lines)
		return 0

	default:
		fmt.Fprintf(opts.out.stderr,
			"dictador desktop: no conozco %q (install | uninstall | status)\n", action)
		return 2
	}
}
