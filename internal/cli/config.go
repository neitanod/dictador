package cli

import (
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"

	"github.com/neitanod/dictador/internal/config"
)

func cmdConfig(opts *options, args []string) int {
	fs := subflags("config", opts, opts.out.stderr)
	force := fs.Bool("force", false, "sobrescribir el config existente")
	fs.BoolVar(force, "f", false, "sobrescribir el config existente")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	opts.refresh()
	rest := fs.Args()
	action := "show"
	if len(rest) > 0 {
		action = rest[0]
		rest = rest[1:]
	}

	switch action {
	case "path":
		fmt.Fprintln(opts.out.stdout, config.ConfigPath())
		return 0

	case "init":
		path, err := config.Init(*force)
		if err != nil {
			opts.out.fail(err, "CONFIG")
			return 1
		}
		_ = opts.out.print(map[string]string{"path": path}, []string{"config en " + path})
		return 0

	case "edit":
		path, err := config.Init(false)
		if err != nil {
			opts.out.fail(err, "CONFIG")
			return 1
		}
		editor := os.Getenv("EDITOR")
		if editor == "" {
			editor = "nano"
		}
		cmd := exec.Command(editor, path)
		cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
		if err := cmd.Run(); err != nil {
			return 1
		}
		return 0

	case "set":
		return configSet(opts, rest)

	case "show":
		cfg, err := opts.load()
		if err != nil {
			opts.out.fail(err, "CONFIG")
			return 1
		}
		// La configuración es una estructura: mostrarla como JSON es más honesto
		// que reimprimir un TOML que no es el que está en disco.
		_ = opts.out.printJSON(cfg)
		return 0

	default:
		fmt.Fprintf(opts.out.stderr,
			"dictador config: no conozco %q (show | init | edit | path | set)\n", action)
		return 2
	}
}

// configSet escribe un valor suelto sin pisar los comentarios del archivo.
//
//	dictador config set stt.engine chrome
func configSet(opts *options, args []string) int {
	if len(args) < 2 {
		fmt.Fprintln(opts.out.stderr,
			"uso: dictador config set <sección.clave> <valor>   (ej: stt.engine chrome)")
		return 2
	}
	key, raw := args[0], strings.Join(args[1:], " ")
	section, name, found := strings.Cut(key, ".")
	if !found || section == "" || name == "" {
		fmt.Fprintf(opts.out.stderr, "dictador: %q tiene que ser sección.clave\n", key)
		return 2
	}

	path := opts.configPath
	if path == "" {
		path = config.ConfigPath()
	}
	written, err := config.Save(path, []config.Setting{
		{Section: section, Key: name, Value: parseValue(raw)},
	})
	if err != nil {
		opts.out.fail(err, "CONFIG")
		return 1
	}
	// Releer es la verificación: si lo que se escribió no parsea, mejor que se
	// entere ahora y no el daemon en el próximo arranque.
	if _, err := config.Load(written); err != nil {
		opts.out.fail(fmt.Errorf("quedó escrito pero el archivo no parsea: %w", err), "CONFIG")
		return 1
	}
	_ = opts.out.print(
		map[string]any{"path": written, "section": section, "key": name, "value": parseValue(raw)},
		[]string{fmt.Sprintf("%s.%s = %v  →  %s", section, name, parseValue(raw), written)},
	)
	return 0
}

// parseValue adivina el tipo del valor que se escribió en la línea de comandos.
func parseValue(raw string) any {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "true":
		return true
	case "false":
		return false
	}
	if n, err := strconv.Atoi(strings.TrimSpace(raw)); err == nil {
		return n
	}
	if f, err := strconv.ParseFloat(strings.TrimSpace(raw), 64); err == nil {
		return f
	}
	return raw
}
