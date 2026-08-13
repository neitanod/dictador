package webconfig

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/neitanod/dictador/internal/config"
	"github.com/neitanod/dictador/internal/stt"
)

func TestQuitCierraElCanal(t *testing.T) {
	s, err := New(config.Config{})
	if err != nil {
		t.Fatalf("no pude levantar el server: %v", err)
	}
	defer s.Close()

	select {
	case <-s.Quit():
		t.Fatal("el canal ya estaba cerrado sin que nadie apretara el botón")
	default:
	}

	rec := httptest.NewRecorder()
	s.handleQuit(rec, httptest.NewRequest(http.MethodPost, "/quit", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("esperaba 200, vino %d", rec.Code)
	}

	select {
	case <-s.Quit():
	default:
		t.Fatal("el POST a /quit no cerró el canal")
	}

	// Dos clicks seguidos no tienen que romper nada: el canal ya está cerrado.
	s.handleQuit(httptest.NewRecorder(), httptest.NewRequest(http.MethodPost, "/quit", nil))
}

func TestAbreConChromeCuandoHay(t *testing.T) {
	s, err := New(config.Config{})
	if err != nil {
		t.Fatalf("no pude levantar el server: %v", err)
	}
	defer s.Close()

	cmds := s.openCommands()
	if len(cmds) == 0 {
		t.Fatal("me quedé sin manera de abrir la página")
	}
	last := cmds[len(cmds)-1]
	if filepath.Base(last.Path) != "xdg-open" {
		t.Fatalf("el último recurso tiene que ser xdg-open, es %q", last.Path)
	}
	if stt.ChromeBinary("") == "" {
		return // sin Chrome en esta máquina no hay ventana de app que pedir
	}
	// La ventana de app es la única que el botón "Matar al dictador" puede
	// cerrar: si dejara de pedirse, la página quedaría abierta para siempre.
	if !strings.Contains(strings.Join(cmds[0].Args, " "), "--app="+s.URL()) {
		t.Fatalf("esperaba una ventana de app de Chrome, vino %v", cmds[0].Args)
	}
}

func TestQuitSoloPorPOST(t *testing.T) {
	s, err := New(config.Config{})
	if err != nil {
		t.Fatalf("no pude levantar el server: %v", err)
	}
	defer s.Close()

	rec := httptest.NewRecorder()
	s.handleQuit(rec, httptest.NewRequest(http.MethodGet, "/quit", nil))
	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("esperaba 405, vino %d", rec.Code)
	}
	select {
	case <-s.Quit():
		t.Fatal("un GET no tiene que matar el programa")
	default:
	}
}
