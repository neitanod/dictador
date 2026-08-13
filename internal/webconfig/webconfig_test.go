package webconfig

import (
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

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
	// Con un perfil virgen —cualquier prueba que corra con HOME temporal— un
	// Chrome sin estos flags abre el diálogo de bienvenida en vez de la página.
	// Van escritos a mano y no leídos de chromeQuiet: la lista es justo lo que
	// esto tiene que custodiar.
	for _, flag := range []string{"--no-first-run", "--no-default-browser-check"} {
		if !slices.Contains(cmds[0].Args, flag) {
			t.Errorf("falta %s: Chrome va a abrir el diálogo de bienvenida, vino %v", flag, cmds[0].Args)
		}
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

// newServer levanta el server contra un config y un HOME de mentira, para que
// guardar escriba en un archivo del test y no en el de la máquina.
func newServer(t *testing.T, cfg config.Config) *Server {
	t.Helper()
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	server, err := New(cfg)
	if err != nil {
		t.Fatalf("no pude levantar el server: %v", err)
	}
	t.Cleanup(server.Close)
	return server
}

func TestLaPáginaMuestraElEstadoDeLosComandos(t *testing.T) {
	cfg := config.Defaults()
	server := newServer(t, cfg)

	body := get(t, server.URL())
	if !strings.Contains(body, "Comandos hablados") {
		t.Error("la página no habla de los comandos")
	}
	if !strings.Contains(body, `id="commands" checked`) {
		t.Error("los comandos están prendidos y el checkbox no lo muestra")
	}

	cfg.Commands.Enabled = false
	server.Update(cfg)
	if body := get(t, server.URL()); strings.Contains(body, `id="commands" checked`) {
		t.Error("los comandos están apagados y el checkbox sigue tildado")
	}
}

func TestGuardarApagaLosComandosEnElArchivoYAvisaAlDaemon(t *testing.T) {
	server := newServer(t, config.Defaults())

	answer, err := http.Post(server.URL()+"save", "application/json",
		strings.NewReader(`{"engine":"whisper","commands":false,
			"screen":"mouse","position":"bottom-center"}`))
	if err != nil {
		t.Fatalf("no pude guardar: %v", err)
	}
	defer answer.Body.Close()
	if answer.StatusCode != http.StatusOK {
		got, _ := io.ReadAll(answer.Body)
		t.Fatalf("guardar dio %d: %s", answer.StatusCode, got)
	}

	select {
	case values := <-server.Saved():
		if values.Commands {
			t.Error("el daemon se enteró de que los comandos siguen prendidos")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("nadie avisó que se guardó")
	}

	raw, err := os.ReadFile(config.ConfigPath())
	if err != nil {
		t.Fatalf("no se escribió el config: %v", err)
	}
	if !strings.Contains(string(raw), "enabled = false") {
		t.Errorf("el archivo no apagó los comandos:\n%s", raw)
	}
}

func get(t *testing.T, url string) string {
	t.Helper()
	answer, err := http.Get(url)
	if err != nil {
		t.Fatalf("no pude leer la página: %v", err)
	}
	defer answer.Body.Close()
	body, err := io.ReadAll(answer.Body)
	if err != nil {
		t.Fatalf("no pude leer la página: %v", err)
	}
	return string(body)
}
