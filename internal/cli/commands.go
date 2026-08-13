package cli

import (
	"fmt"
	"strings"

	"github.com/neitanod/dictador/internal/commands"
)

// cmdCommands lista los comandos hablados que van a andar con tu configuración,
// o prueba una frase contra ellos sin tener que hablarle al micrófono.
func cmdCommands(opts *options, args []string) int {
	fs := subflags("commands", opts, opts.out.stderr)
	try := fs.String("try", "", "compilar esta frase en vez de listar")
	fs.StringVar(try, "t", "", "compilar esta frase en vez de listar")
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
	options := commands.OptionsFrom(cfg)

	if *try != "" {
		plan := commands.Compile(*try, options)
		lines := []string{plan.Text()}
		if plan.HasKeys() {
			lines = append(lines, "", "pasos:")
			for _, step := range plan.Steps {
				if step.Key != "" {
					lines = append(lines, "  tecla  "+step.Key)
					continue
				}
				lines = append(lines, fmt.Sprintf("  texto  %q", step.Text))
			}
		}
		_ = o.print(map[string]any{"text": plan.Text(), "steps": plan.Steps}, lines)
		return 0
	}

	list := commands.List(options)
	if !cfg.Commands.Enabled {
		o.info("ojo: los comandos están apagados (commands.enabled = false)")
	}
	lines := make([]string, 0, len(list))
	for _, c := range list {
		lines = append(lines, fmt.Sprintf("%-24s %s", c.Say, effect(c)))
	}
	_ = o.print(map[string]any{
		"enabled":  cfg.Commands.Enabled,
		"commands": list,
	}, lines)
	return 0
}

// effect es la columna derecha del listado: qué pasa cuando lo decís.
func effect(c commands.Command) string {
	var parts []string
	switch {
	case c.Writes == " ":
		parts = append(parts, "escribe un espacio")
	case c.Writes == "  ":
		parts = append(parts, "escribe dos espacios")
	case c.Writes != "":
		parts = append(parts, "escribe "+c.Writes)
	}
	for _, key := range c.Keys {
		if key == "Left" {
			continue // lo cuenta la nota
		}
		parts = append(parts, "manda "+key)
	}
	line := strings.Join(parts, ", ")
	if c.Note != "" {
		// La nota de los pares ya viene con su "y" adelante y no lleva coma.
		switch {
		case line == "":
			line = c.Note
		case strings.HasPrefix(c.Note, "y "):
			line += " " + c.Note
		default:
			line += ", " + c.Note
		}
	}
	if c.Custom {
		line += "   (tuyo)"
	}
	return line
}
