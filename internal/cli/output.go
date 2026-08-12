package cli

import (
	"encoding/json"
	"fmt"
	"io"
)

// out lleva a dónde escribe la CLI. Está acá y no en variables globales para
// que los tests puedan mirarlo.
type out struct {
	stdout io.Writer
	stderr io.Writer
	json   bool
	quiet  bool
}

// printJSON vuelca el resultado tal cual, para que lo consuma una máquina.
func (o out) printJSON(data any) error {
	enc := json.NewEncoder(o.stdout)
	enc.SetIndent("", "  ")
	enc.SetEscapeHTML(false)
	return enc.Encode(data)
}

// print elige entre el JSON y las líneas para humanos.
func (o out) print(data any, lines []string) error {
	if o.json {
		return o.printJSON(data)
	}
	for _, line := range lines {
		fmt.Fprintln(o.stdout, line)
	}
	return nil
}

// info es para lo que acompaña al resultado y no es el resultado: se calla con
// --quiet y no existe en modo JSON.
func (o out) info(format string, args ...any) {
	if o.json || o.quiet {
		return
	}
	fmt.Fprintf(o.stderr, format+"\n", args...)
}

// fail escribe el error en el formato que corresponda.
func (o out) fail(err error, code string) {
	if o.json {
		_ = json.NewEncoder(o.stderr).Encode(map[string]string{
			"error": err.Error(),
			"code":  code,
		})
		return
	}
	fmt.Fprintf(o.stderr, "dictador: %s\n", err)
}
