// Package cli es la capa fina que traduce argumentos a llamadas y resultados a
// texto o JSON. Toda la lógica vive en los paquetes de internal/.
package cli

import (
	"flag"
	"fmt"
	"io"
	"strings"

	"github.com/neitanod/dictador/internal/config"
)

// Version se pisa en el build con -ldflags "-X ...cli.Version=…".
var Version = "dev"

const usage = `dictador — dictado por voz global para Linux/X11: mantené la tecla, hablá, soltá.

uso: dictador [flags globales] <comando> [flags del comando]

comandos:
  run        arranca el daemon con la tecla global (es lo que hace sin comando)
  once       graba una vez y escribe el texto en stdout
  bench      compara los motores con tu voz para elegir el tuyo
  doctor     chequea que todo lo necesario esté en su lugar
  keys       lista las teclas del mapa actual, para elegir el hotkey
  config     ver o editar la configuración
  history    últimos dictados
  service    autostart en el login
  version    qué versión es esta

flags globales:
  --config <ruta>   usar otro config.toml
  --json            salida en JSON
  --verbose         contar lo que va pasando
  --quiet           sólo lo esencial

exit codes: 0 salió bien · 1 error · 2 error de uso · 130 cancelado con Ctrl+C
`

// options son los flags globales, ya resueltos.
type options struct {
	configPath string
	json       bool
	verbose    bool
	quiet      bool
	out        out
}

// Main corre la CLI y devuelve el exit code.
func Main(args []string, stdout, stderr io.Writer) int {
	global := flag.NewFlagSet("dictador", flag.ContinueOnError)
	global.SetOutput(stderr)
	global.Usage = func() { fmt.Fprint(stderr, usage) }

	opts := options{}
	global.StringVar(&opts.configPath, "config", "", "ruta alternativa al config.toml")
	global.BoolVar(&opts.json, "json", false, "salida en JSON")
	global.BoolVar(&opts.json, "j", false, "salida en JSON")
	global.BoolVar(&opts.verbose, "verbose", false, "contar lo que va pasando")
	global.BoolVar(&opts.quiet, "quiet", false, "sólo lo esencial")
	global.BoolVar(&opts.quiet, "q", false, "sólo lo esencial")
	showVersion := global.Bool("version", false, "mostrar la versión y salir")

	if err := global.Parse(args); err != nil {
		return 2
	}
	opts.out = out{stdout: stdout, stderr: stderr, json: opts.json, quiet: opts.quiet}

	if *showVersion {
		fmt.Fprintf(stdout, "dictador %s\n", Version)
		return 0
	}

	rest := global.Args()
	command := "run" // sin comando, dictar es lo que uno quiere
	if len(rest) > 0 {
		command = rest[0]
		rest = rest[1:]
	}

	switch command {
	case "run":
		return cmdRun(&opts, rest)
	case "once":
		return cmdOnce(&opts, rest)
	case "bench":
		return cmdBench(&opts, rest)
	case "doctor":
		return cmdDoctor(&opts, rest)
	case "keys":
		return cmdKeys(&opts, rest)
	case "config":
		return cmdConfig(&opts, rest)
	case "history":
		return cmdHistory(&opts, rest)
	case "service":
		return cmdService(&opts, rest)
	case "version":
		fmt.Fprintf(stdout, "dictador %s\n", Version)
		return 0
	case "help", "-h", "--help":
		fmt.Fprint(stdout, usage)
		return 0
	default:
		fmt.Fprintf(stderr, "dictador: no conozco el comando %q\n\n", command)
		fmt.Fprint(stderr, usage)
		return 2
	}
}

// load trae la configuración con la que va a trabajar el comando.
func (o *options) load() (config.Config, error) {
	return config.Load(o.configPath)
}

// subflags arma el FlagSet de un subcomando.
//
// Los flags globales se registran también acá: la convención dice que van antes
// del subcomando, y nadie se acuerda de eso a las tres de la mañana. Escritos
// después ganan, que es lo que uno espera de lo más específico.
func subflags(name string, opts *options, stderr io.Writer) *flag.FlagSet {
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.StringVar(&opts.configPath, "config", opts.configPath, "ruta alternativa al config.toml")
	fs.BoolVar(&opts.json, "json", opts.json, "salida en JSON")
	fs.BoolVar(&opts.json, "j", opts.json, "salida en JSON")
	fs.BoolVar(&opts.verbose, "verbose", opts.verbose, "contar lo que va pasando")
	fs.BoolVar(&opts.verbose, "v", opts.verbose, "contar lo que va pasando")
	fs.BoolVar(&opts.quiet, "quiet", opts.quiet, "sólo lo esencial")
	fs.BoolVar(&opts.quiet, "q", opts.quiet, "sólo lo esencial")
	return fs
}

// refresh vuelve a armar la salida después de parsear los flags del subcomando,
// que pueden haber prendido --json o --quiet.
func (o *options) refresh() {
	o.out.json = o.json
	o.out.quiet = o.quiet
}

// firstArg devuelve el primer argumento suelto, o el default.
func firstArg(args []string, def string) string {
	for _, a := range args {
		if !strings.HasPrefix(a, "-") {
			return a
		}
	}
	return def
}
