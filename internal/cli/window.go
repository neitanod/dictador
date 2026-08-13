package cli

import (
	"fmt"
	"strings"
	"time"

	"github.com/neitanod/dictador/internal/x11"
)

// cmdWindow dice qué ve el dictador cuando mira la ventana de enfrente.
//
// Existe por una sola pregunta, que se repite cada vez que aparece una terminal
// nueva: "no me pega acá, ¿qué le pongo en terminal_classes?". La clase es la
// respuesta, y hasta ahora había que ir a buscarla con xprop.
func cmdWindow(opts *options, args []string) int {
	fs := subflags("window", opts, opts.out.stderr)
	wait := fs.Float64("wait", 0, "esperar N segundos antes de mirar, para cambiar de ventana")
	fs.Float64Var(wait, "w", 0, "esperar N segundos antes de mirar, para cambiar de ventana")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	opts.refresh()
	o := opts.out

	cfg, err := opts.load()
	if err != nil {
		o.fail(err, "CONFIG")
		return 1
	}

	if *wait > 0 {
		if !o.quiet && !o.json {
			fmt.Fprintf(o.stdout, "enfocá la ventana que querés mirar: %.0fs\n", *wait)
		}
		time.Sleep(time.Duration(*wait * float64(time.Second)))
	}

	conn, err := x11.Open()
	if err != nil {
		o.fail(err, "X11")
		return 1
	}
	defer conn.Close()

	target := conn.ActiveWindow()
	if target.Window == 0 {
		o.fail(fmt.Errorf("no hay ninguna ventana activa"), "X11")
		return 1
	}
	terminal := x11.IsTerminalWith(target.Class, cfg.Action.TerminalClasses)
	combo := "ctrl+v"
	if terminal {
		combo = "ctrl+shift+v"
	}

	lines := []string{
		fmt.Sprintf("ventana   %d", target.Window),
		fmt.Sprintf("clase     %s", orUnknown(target.Class)),
		fmt.Sprintf("terminal  %v", terminal),
		fmt.Sprintf("pega con  %s", combo),
	}
	if !terminal {
		known := append([]string{}, cfg.Action.TerminalClasses...)
		known = append(known, target.Class)
		lines = append(lines, "",
			"Si esto es una terminal y el dictado no entra, sumá su clase al config:",
			fmt.Sprintf("  dictador config set action.terminal_classes '[%s]'", strings.Join(known, ", ")))
	}
	_ = o.print(map[string]any{
		"window": target.Window, "class": target.Class,
		"terminal": terminal, "combo": combo,
	}, lines)
	return 0
}

func orUnknown(s string) string {
	if s == "" {
		return "(sin WM_CLASS)"
	}
	return s
}
