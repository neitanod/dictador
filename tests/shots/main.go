// Comando shots: abre el overlay en cada uno de sus estados y saca capturas.
//
// El overlay es la única parte del port que se juzga con el ojo y no con un
// test, así que lo mínimo es poder verlo sin tener que dictar. Corre contra un
// Xvfb y deja los PNG donde se le diga.
//
//	shots <directorio>
package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	"github.com/neitanod/dictador/internal/config"
	"github.com/neitanod/dictador/internal/overlay"
)

func main() {
	dir := "tests/shots"
	if len(os.Args) > 1 {
		dir = os.Args[1]
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		fail(err)
	}

	cfg := config.Defaults().Overlay
	// Abajo, como en la vida real: en el display virtual el puntero arranca en
	// el centro de la pantalla, y si la ventanita cayera ahí las capturas
	// saldrían todas en estado hover.
	cfg.Position = "bottom-center"
	window, err := overlay.NewWindow(cfg)
	if err != nil {
		fail(err)
	}
	defer window.Close()

	shot := func(name string, setup func()) {
		setup()
		time.Sleep(600 * time.Millisecond)
		path := filepath.Join(dir, name+".png")
		out, err := exec.Command("import", "-window", "root", path).CombinedOutput()
		if err != nil {
			fmt.Fprintf(os.Stderr, "no pude capturar %s: %v %s\n", name, err, out)
			return
		}
		fmt.Println(path)
	}

	shot("01-escuchando", func() {
		window.BeginListening("Escuchando…")
		window.SetMeter(0.55, 2.4)
	})
	shot("02-parcial", func() {
		window.SetPartial("esto es lo que se va entendiendo mientras hablás, y sigue " +
			"creciendo hasta que la ventanita necesita una segunda línea para mostrarlo entero")
		window.SetMeter(0.8, 5.1)
	})
	shot("03-transcribiendo", func() {
		window.SetThinking("Transcribiendo 5.1s…")
	})
	shot("04-resultado", func() {
		window.SetDone("esto es lo que se va entendiendo mientras hablás", "Pegado", 0)
	})
	shot("05-error", func() {
		window.SetError("Chrome no pudo hablar con el servicio de voz de Google (¿hay internet?)")
	})
}

func fail(err error) {
	fmt.Fprintln(os.Stderr, "shots:", err)
	os.Exit(1)
}
