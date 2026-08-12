// dictador — dictado por voz global para Linux/X11.
//
// Mantené la tecla, hablá, soltala, y el texto aparece donde tenías el cursor.
package main

import (
	"os"

	"github.com/neitanod/dictador/internal/cli"
)

func main() {
	os.Exit(cli.Main(os.Args[1:], os.Stdout, os.Stderr))
}
