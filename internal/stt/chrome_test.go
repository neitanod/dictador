package stt

import (
	"testing"
	"time"

	"github.com/neitanod/dictador/internal/config"
)

// Un dictador que muere de mala manera —kill -9, un cuelgue, la sesión que se
// cierra— no llega a correr Close(), y el Chrome que había lanzado quedaba vivo
// para siempre: headless, invisible, reintentando contra un puerto muerto. La
// página se tiene que dar cuenta sola y cerrarse.
//
// Es un test con Chrome de verdad porque lo que está en duda es justamente si
// window.close() alcanza para terminar el proceso: eso no se puede afirmar
// leyendo el código.
func TestChromeSeCierraSoloCuandoElServerDesaparece(t *testing.T) {
	if testing.Short() {
		t.Skip("lanza un Chrome de verdad")
	}
	if ChromeBinary("") == "" {
		t.Skip("no hay Chrome en esta máquina")
	}

	chrome := NewChrome(Options{STT: config.STT{
		ChromeHeadless:      true,
		ChromeReadyTimeoutS: 30,
	}})
	// Dos reintentos de 500ms en vez de los 20 segundos de la vida real.
	chrome.orphanRetries = 2
	defer chrome.Close()

	if err := chrome.Load(); err != nil {
		t.Skipf("este Chrome no levantó la página de dictado: %v", err)
	}

	// Matar el server sin tocar el proceso es exactamente lo que Chrome ve
	// cuando el daemon se muere sin despedirse.
	chrome.mu.Lock()
	server, listener, exited := chrome.server, chrome.listener, chrome.exited
	chrome.server, chrome.listener = nil, nil
	chrome.mu.Unlock()
	_ = server.Close()
	_ = listener.Close()

	select {
	case <-exited:
	case <-time.After(30 * time.Second):
		t.Error("Chrome siguió vivo con el dictador muerto: quedaría un huérfano")
	}
}
