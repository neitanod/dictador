package overlay

import (
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/jezek/xgb/xproto"
	"github.com/neitanod/dictador/internal/config"
)

// El click en la ventanita abre la configuración, y la ventanita se tiene que
// ir de la pantalla. Es el mismo click el que dispara las dos cosas, así que
// cuando llega el Dismiss suele haber un cuadro a medio pintar en camino: si
// ese cuadro termina mapeando la ventana, la ventanita ya no se va nunca más.
func TestClickCierraLaVentanita(t *testing.T) {
	w := testWindow(t)

	for vuelta := 1; vuelta <= 10; vuelta++ {
		w.BeginListening("probando")
		waitMapped(t, w, 1, fmt.Sprintf("vuelta %d: la ventanita no apareció", vuelta))

		click(t, w)
		select {
		case <-w.Clicked():
		case <-time.After(2 * time.Second):
			t.Fatalf("vuelta %d: el click nunca llegó", vuelta)
		}
		// Esto es lo que hace el daemon cuando le llega el click.
		w.Dismiss()
		waitMapped(t, w, 0, fmt.Sprintf("vuelta %d: la ventanita quedó en pantalla después del click", vuelta))
	}
}

// El click hace dos cosas a la vez: pide un redibujo —para sacarle el resaltado
// del mouse— y le avisa al daemon, que baja la ventanita. Así que cuando llega
// el Dismiss casi siempre hay un cuadro a medio armar en camino, armado con el
// estado de antes. Si ese cuadro termina y mapea la ventana igual, deshace el
// Dismiss y la ventanita se queda en pantalla para siempre: nadie va a pedir
// otro redibujo que la baje.
func TestElCuadroEnVueloNoRevivelaVentanita(t *testing.T) {
	w := testWindow(t)

	w.BeginListening("probando")
	waitMapped(t, w, 1, "la ventanita no apareció")

	// El cuadro que salió en camino con el click: leyó el estado de antes.
	w.mu.Lock()
	current, screen, position := w.current, w.screen, w.position
	w.mu.Unlock()

	w.Dismiss()
	w.drawFrame(current, screen, position) // y recién ahora termina

	waitMapped(t, w, 0, "el cuadro en vuelo volvió a poner la ventanita en pantalla")
}

// Y lo mismo cuando el plazo de "listo" se cumple solo, sin que nadie clickee.
func TestSeVaSolaCuandoSeCumpleElPlazo(t *testing.T) {
	w := testWindow(t)

	w.SetDone("hola", "listo", 150*time.Millisecond)
	waitMapped(t, w, 1, "la ventanita no apareció")
	waitMapped(t, w, 0, "la ventanita se quedó en pantalla pasado el plazo")
}

func testWindow(t *testing.T) *Window {
	t.Helper()
	if os.Getenv("DISPLAY") == "" {
		t.Skip("sin DISPLAY: hace falta un servidor X corriendo")
	}
	cfg := config.Defaults().Overlay
	// Abajo, lejos del puntero: en el display virtual el mouse arranca en el
	// centro, y una ventanita ahí abajo no queda en estado hover.
	cfg.Position = "bottom-center"
	w, err := NewWindow(cfg)
	if err != nil {
		t.Skipf("acá no se puede dibujar el overlay: %v", err)
	}
	t.Cleanup(w.Close)
	return w
}

// click le manda un ButtonPress a la primera ventana, como el mouse de verdad.
func click(t *testing.T, w *Window) {
	t.Helper()
	windows := windowIDs(w)
	if len(windows) == 0 {
		t.Fatal("no hay ninguna ventana a la que hacerle click")
	}
	event := xproto.ButtonPressEvent{
		Detail:     1,
		Time:       xproto.TimeCurrentTime,
		Root:       w.conn.Root,
		Event:      windows[0],
		EventX:     1,
		EventY:     1,
		SameScreen: true,
	}
	err := xproto.SendEventChecked(w.conn.X, false, windows[0],
		xproto.EventMaskButtonPress, string(event.Bytes())).Check()
	if err != nil {
		t.Fatalf("no pude mandar el click: %v", err)
	}
}

// waitMapped espera hasta dos segundos a que haya esa cantidad de ventanas en
// pantalla. El dibujo es asincrónico, así que la única forma de mirarlo es
// preguntarle al servidor X hasta que conteste lo que se espera.
func waitMapped(t *testing.T, w *Window, want int, message string) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	got := -1
	for time.Now().Before(deadline) {
		if got = mappedCount(t, w); got == want {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("%s: esperaba %d ventana(s) en pantalla, hay %d", message, want, got)
}

func mappedCount(t *testing.T, w *Window) int {
	t.Helper()
	count := 0
	for _, win := range windowIDs(w) {
		attrs, err := xproto.GetWindowAttributes(w.conn.X, win).Reply()
		if err != nil {
			t.Fatalf("no pude preguntar por la ventana: %v", err)
		}
		if attrs.MapState == xproto.MapStateViewable {
			count++
		}
	}
	return count
}

func windowIDs(w *Window) []xproto.Window {
	w.mu.Lock()
	defer w.mu.Unlock()
	ids := make([]xproto.Window, 0, len(w.surfaces))
	for _, s := range w.surfaces {
		ids = append(ids, s.win)
	}
	return ids
}
