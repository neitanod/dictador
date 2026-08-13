package webconfig

import (
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
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

func TestLaPáginaMuestraElEspacioAlFinal(t *testing.T) {
	cfg := config.Defaults()
	server := newServer(t, cfg)

	body := get(t, server.URL())
	if !strings.Contains(body, "Dejar un espacio al final") {
		t.Error("la página no ofrece el espacio al final")
	}
	// La explicación es la mitad del pedido: sin ella el checkbox es un misterio.
	if !strings.Contains(body, "cada uno arranca separado del anterior") {
		t.Error("la página no explica para qué sirve el espacio al final")
	}
	if strings.Contains(body, `id="trailing" checked`) {
		t.Error("el espacio al final está apagado y el checkbox aparece tildado")
	}

	cfg.Action.TrailingSpace = true
	server.Update(cfg)
	if body := get(t, server.URL()); !strings.Contains(body, `id="trailing" checked`) {
		t.Error("el espacio al final está prendido y el checkbox no lo muestra")
	}
}

func TestGuardarPrendeElEspacioAlFinalEnElArchivoYAvisaAlDaemon(t *testing.T) {
	server := newServer(t, config.Defaults())

	answer, err := http.Post(server.URL()+"save", "application/json",
		strings.NewReader(`{"engine":"whisper","commands":true,"trailing_space":true,
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
		if !values.TrailingSpace {
			t.Error("el daemon no se enteró del espacio al final")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("nadie avisó que se guardó")
	}

	raw, err := os.ReadFile(config.ConfigPath())
	if err != nil {
		t.Fatalf("no se escribió el config: %v", err)
	}
	if !strings.Contains(string(raw), "trailing_space = true") {
		t.Errorf("el archivo no prendió el espacio al final:\n%s", raw)
	}
}

func TestLaPáginaLinkeaAlConfigToml(t *testing.T) {
	server := newServer(t, config.Defaults())

	body := get(t, server.URL())
	if !strings.Contains(body, `id="edit"`) {
		t.Error("la ruta del config.toml no es un link para abrirlo")
	}
	// La ruta se sigue viendo entera: es lo que hay que copiar cuando la máquina
	// no tiene con qué abrirla.
	if !strings.Contains(body, config.ConfigPath()) {
		t.Errorf("la página no muestra la ruta del config:\n%s", body)
	}
}

func TestElLinkAbreElConfigYLoCreaSiFalta(t *testing.T) {
	cfg := config.Defaults()
	cfg.Path = filepath.Join(t.TempDir(), "config.toml")
	server := newServer(t, cfg)

	var opened string
	server.edit = func(path string) error {
		opened = path
		return nil
	}

	answer, err := http.Post(server.URL()+"edit", "application/json", nil)
	if err != nil {
		t.Fatalf("no pude pedir el editor: %v", err)
	}
	defer answer.Body.Close()
	if answer.StatusCode != http.StatusOK {
		got, _ := io.ReadAll(answer.Body)
		t.Fatalf("abrir el editor dio %d: %s", answer.StatusCode, got)
	}
	if opened != cfg.Path {
		t.Errorf("abrió %q en vez del config configurado %q", opened, cfg.Path)
	}
	// Un editor abriendo un archivo que no existe es una hoja en blanco sin los
	// comentarios que explican cada valor: primero se escribe la plantilla.
	raw, err := os.ReadFile(cfg.Path)
	if err != nil {
		t.Fatalf("el config no quedó escrito antes de abrirlo: %v", err)
	}
	if !strings.Contains(string(raw), "[stt]") {
		t.Errorf("el config que se abre no tiene la plantilla:\n%s", raw)
	}
}

func TestSiNoHayConQuéAbrirElConfigLaPáginaSeEntera(t *testing.T) {
	server := newServer(t, config.Defaults())
	server.edit = func(string) error { return errors.New("no encontré editor") }

	answer, err := http.Post(server.URL()+"edit", "application/json", nil)
	if err != nil {
		t.Fatalf("no pude pedir el editor: %v", err)
	}
	defer answer.Body.Close()
	if answer.StatusCode != http.StatusInternalServerError {
		t.Fatalf("esperaba 500, vino %d", answer.StatusCode)
	}
	body, _ := io.ReadAll(answer.Body)
	if !strings.Contains(string(body), "no encontré editor") {
		t.Errorf("la página no se entera de por qué falló: %s", body)
	}
}

func TestElEditorSeBuscaEntreLosQueHaya(t *testing.T) {
	t.Setenv("VISUAL", "")
	t.Setenv("EDITOR", "")
	path := filepath.Join(t.TempDir(), "config.toml")

	cmds := editCommands(path)
	if len(cmds) == 0 {
		t.Fatal("me quedé sin manera de abrir el config")
	}
	for _, cmd := range cmds {
		if !slices.Contains(cmd.Args, path) {
			t.Errorf("%v no abre el config", cmd.Args)
		}
	}
	// xdg-open abre con lo que el escritorio tenga asociado a la extensión, y en
	// una Ubuntu de todos los días un .toml lo abre LibreOffice Writer. Va
	// después de cualquier editor de texto de verdad que esté instalado.
	xdg := slices.IndexFunc(cmds, func(cmd *exec.Cmd) bool {
		return filepath.Base(cmd.Path) == "xdg-open"
	})
	if xdg < 0 {
		t.Fatalf("xdg-open no está entre los intentos: %v", cmds)
	}
	for _, editor := range guiEditors {
		if _, err := exec.LookPath(editor); err != nil {
			continue
		}
		antes := slices.ContainsFunc(cmds[:xdg], func(cmd *exec.Cmd) bool {
			return filepath.Base(cmd.Path) == editor
		})
		if !antes {
			t.Errorf("%s está instalado y se prueba después de xdg-open", editor)
		}
	}
}

func TestElEditorQueElUsuarioEligióVaPrimero(t *testing.T) {
	t.Setenv("VISUAL", "mi-editor")
	path := filepath.Join(t.TempDir(), "config.toml")

	cmds := editCommands(path)
	if filepath.Base(cmds[0].Path) != "mi-editor" {
		t.Errorf("VISUAL dice mi-editor y el primer intento es %q", cmds[0].Path)
	}
	if !slices.Contains(cmds[0].Args, path) {
		t.Errorf("%v no abre el config", cmds[0].Args)
	}
}

func TestElEditorDeConsolaSeAbreEnUnaTerminal(t *testing.T) {
	t.Setenv("VISUAL", "")
	t.Setenv("EDITOR", "vim")
	path := filepath.Join(t.TempDir(), "config.toml")

	// Un vim lanzado suelto muere sin que nadie vea nada, así que va adentro de
	// una terminal: para eso existe la lista de los que son de consola.
	joined := strings.Join(editCommands(path)[0].Args, " ")
	if !strings.Contains(joined, "-e vim "+path) {
		t.Errorf("vim tiene que abrirse en una terminal, vino %q", joined)
	}
}

func TestElÚltimoRecursoEsUnaTerminal(t *testing.T) {
	t.Setenv("VISUAL", "")
	t.Setenv("EDITOR", "un-editor-de-consola")
	path := filepath.Join(t.TempDir(), "config.toml")

	cmds := editCommands(path)
	joined := strings.Join(cmds[len(cmds)-1].Args, " ")
	if !strings.Contains(joined, "-e un-editor-de-consola "+path) {
		t.Errorf("el último intento tendría que ser la terminal, vino %q", joined)
	}
}

// TestElQueSeMuereLeDejaElTurnoAlSiguiente arma un PATH de mentira donde el
// primer editor se cae apenas arranca —lo que pasa de verdad cuando el que está
// instalado no encuentra display— y comprueba que el que sigue en la lista
// termina abriendo el archivo.
func TestElQueSeMuereLeDejaElTurnoAlSiguiente(t *testing.T) {
	t.Setenv("VISUAL", "")
	t.Setenv("EDITOR", "")
	bin := t.TempDir()
	fakeBinary(t, bin, "gnome-text-editor", "exit 3")
	opened := filepath.Join(t.TempDir(), "abierto")
	// El sleep va con ruta entera: adentro del PATH de mentira no hay nada más.
	fakeBinary(t, bin, "gedit", "echo \"$1\" > "+opened+"; /bin/sleep 5")
	t.Setenv("PATH", bin)

	old := editorGrace
	editorGrace = 200 * time.Millisecond
	t.Cleanup(func() { editorGrace = old })

	path := filepath.Join(t.TempDir(), "config.toml")
	if err := openEditor(path); err != nil {
		t.Fatalf("no pude abrir el config: %v", err)
	}
	raw, err := os.ReadFile(opened)
	if err != nil {
		t.Fatalf("nadie abrió el archivo: %v", err)
	}
	if strings.TrimSpace(string(raw)) != path {
		t.Errorf("abrieron %q en vez del config %q", strings.TrimSpace(string(raw)), path)
	}
}

func TestSiSeMuerenTodosLoDice(t *testing.T) {
	bin := t.TempDir()
	for _, name := range append([]string{"xdg-open"}, guiEditors...) {
		fakeBinary(t, bin, name, "exit 3")
	}
	fakeBinary(t, bin, "x-terminal-emulator", "exit 3")
	fakeBinary(t, bin, "nano", "exit 3")
	t.Setenv("PATH", bin)
	t.Setenv("VISUAL", "")
	t.Setenv("EDITOR", "")

	old := editorGrace
	editorGrace = 200 * time.Millisecond
	t.Cleanup(func() { editorGrace = old })

	if err := openEditor(filepath.Join(t.TempDir(), "config.toml")); err == nil {
		t.Error("todos los editores fallaron y openEditor dijo que anduvo")
	}
}

func fakeBinary(t *testing.T, dir, name, body string) {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"+body+"\n"), 0o755); err != nil {
		t.Fatalf("no pude escribir el %s de mentira: %v", name, err)
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
