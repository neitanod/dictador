package cli

import (
	"fmt"
	"os"
	"path/filepath"
)

// El autostart va por el .desktop de XDG, que es lo que KDE, GNOME y Xfce leen
// igual. Una unit de systemd --user también andaría, y traería una dependencia
// del orden de arranque de la sesión gráfica que este .desktop no tiene.
const desktopEntry = `[Desktop Entry]
Type=Application
Name=Dictador
Comment=Dictado por voz global (push-to-talk)
Exec=%s run
Icon=audio-input-microphone
Terminal=false
X-GNOME-Autostart-enabled=true
`

func autostartPath() string {
	home, err := os.UserHomeDir()
	if err != nil {
		home = "."
	}
	return filepath.Join(home, ".config", "autostart", "dictador.desktop")
}

func cmdService(opts options, args []string) int {
	fs := subflags("service", opts, opts.out.stderr)
	if err := fs.Parse(args); err != nil {
		return 2
	}
	action := firstArg(fs.Args(), "status")
	path := autostartPath()

	switch action {
	case "install":
		binary, err := os.Executable()
		if err != nil {
			opts.out.fail(err, "SERVICE")
			return 1
		}
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			opts.out.fail(err, "SERVICE")
			return 1
		}
		if err := os.WriteFile(path, []byte(fmt.Sprintf(desktopEntry, binary)), 0o644); err != nil {
			opts.out.fail(err, "SERVICE")
			return 1
		}
		_ = opts.out.print(map[string]any{"installed": true, "path": path}, []string{
			"autostart instalado en " + path,
			"Arranca solo en el próximo login. Para probarlo ahora: dictador run",
		})
		return 0

	case "uninstall":
		if err := os.Remove(path); err != nil {
			if os.IsNotExist(err) {
				_ = opts.out.print(map[string]any{"installed": false}, []string{"no estaba instalado"})
				return 0
			}
			opts.out.fail(err, "SERVICE")
			return 1
		}
		_ = opts.out.print(map[string]any{"installed": false, "path": path},
			[]string{"borré " + path})
		return 0

	case "status":
		_, err := os.Stat(path)
		installed := err == nil
		_ = opts.out.print(map[string]any{"installed": installed, "path": path},
			[]string{ifElse(installed, "instalado", "no instalado")})
		return 0

	default:
		fmt.Fprintf(opts.out.stderr,
			"dictador service: no conozco %q (install | uninstall | status)\n", action)
		return 2
	}
}
