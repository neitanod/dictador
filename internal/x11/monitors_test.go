package x11

import (
	"os"
	"testing"
)

// Cada conexión a X arranca sin extensiones cargadas: RANDR hay que
// inicializarlo en cada una. La app abre varias —el daemon, el overlay, la
// ventana de configuración— así que preguntar por los monitores en la segunda
// tiene que funcionar igual que en la primera.
func TestMonitorsOnEveryConnection(t *testing.T) {
	if os.Getenv("DISPLAY") == "" {
		t.Skip("sin DISPLAY: hace falta un servidor X corriendo")
	}
	for i := 1; i <= 2; i++ {
		conn, err := Open()
		if err != nil {
			t.Fatalf("conexión %d: %v", i, err)
		}
		monitors := conn.Monitors()
		conn.Close()
		if len(monitors) == 0 {
			t.Fatalf("conexión %d: no devolvió ninguna pantalla", i)
		}
	}
}
