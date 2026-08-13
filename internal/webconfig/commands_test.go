package webconfig

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/neitanod/dictador/internal/commands"
	"github.com/neitanod/dictador/internal/config"
)

// serverOn levanta el server sobre un config.toml de mentira y devuelve los dos.
func serverOn(t *testing.T, body string) (*Server, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("no pude escribir el config de prueba: %v", err)
	}
	cfg, err := config.Load(path)
	if err != nil {
		t.Fatalf("no pude leer el config de prueba: %v", err)
	}
	cfg.Path = path
	s, err := New(cfg)
	if err != nil {
		t.Fatalf("no pude levantar el server: %v", err)
	}
	t.Cleanup(s.Close)
	return s, path
}

func post(t *testing.T, s *Server, body string) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/commands", strings.NewReader(body))
	s.handleCommands(rec, req)
	return rec
}

func TestCommandsListaLoQueVaAAndar(t *testing.T) {
	s, _ := serverOn(t, "[commands]\nenabled = true\n\n[commands.replacements]\n\"punto final\" = \".\"\n")

	rec := httptest.NewRecorder()
	s.handleCommands(rec, httptest.NewRequest(http.MethodGet, "/commands", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("esperaba 200, vino %d: %s", rec.Code, rec.Body)
	}
	var body struct {
		Entries []commands.Entry `json:"entries"`
		Enabled bool             `json:"enabled"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("no entendí la respuesta: %v", err)
	}
	if !body.Enabled {
		t.Fatal("los comandos estaban prendidos y la ventana no se enteró")
	}
	var mine, factory int
	for _, e := range body.Entries {
		if e.Changed {
			mine++
		}
		if e.Builtin {
			factory++
		}
	}
	if mine != 1 {
		t.Fatalf("esperaba un comando mío, vinieron %d", mine)
	}
	if factory < 10 {
		t.Fatalf("la ventana tiene que traer también los de fábrica, vinieron %d", factory)
	}
}

// La página es una plantilla y el editor vive adentro: si alguien le cambia un
// nombre a un campo de la vista, el error sale acá y no en la cara del que abrió
// la configuración.
func TestLaPaginaTraeElBotonYLaVentana(t *testing.T) {
	s, _ := serverOn(t, "[commands]\nenabled = true\n")
	rec := httptest.NewRecorder()
	s.handlePage(rec, httptest.NewRequest(http.MethodGet, "/", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("esperaba 200, vino %d", rec.Code)
	}
	page := rec.Body.String()
	for _, want := range []string{`id="openCommands"`, `id="commandsDialog"`, `id="commandsRows"`} {
		if !strings.Contains(page, want) {
			t.Fatalf("la página no trae %s", want)
		}
	}
}

func TestCommandsGuardaEnElConfig(t *testing.T) {
	s, path := serverOn(t, "[commands]\nenabled = true\n")

	rec := post(t, s, `{"replacements":[
		{"say":"punto final","writes":"."},
		{"say":"palabra coma","writes":","},
		{"say":"coma","writes":""}
	]}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("esperaba 200, vino %d: %s", rec.Code, rec.Body)
	}

	saved, err := config.Load(path)
	if err != nil {
		t.Fatalf("dejé el config roto: %v", err)
	}
	if saved.Commands.Replacements["punto final"] != "." {
		t.Fatalf("no se guardó lo que mandé: %v", saved.Commands.Replacements)
	}
	if _, ok := saved.Commands.Replacements["coma"]; !ok {
		t.Fatalf("la coma apagada tiene que quedar escrita en vacío: %v", saved.Commands.Replacements)
	}

	// Y el dictado ya tiene que salir con eso, sin reiniciar nada.
	if got := commands.Compile("hola punto final", commands.OptionsFrom(saved)).Text(); got != "hola." {
		t.Fatalf("esperaba \"hola.\", vino %q", got)
	}
}

func TestCommandsAvisaAlDaemon(t *testing.T) {
	s, _ := serverOn(t, "[commands]\nenabled = true\n")
	if rec := post(t, s, `{"replacements":[{"say":"flecha","writes":"→"}]}`); rec.Code != http.StatusOK {
		t.Fatalf("esperaba 200, vino %d: %s", rec.Code, rec.Body)
	}
	select {
	case values := <-s.Saved():
		if values.Replacements["flecha"] != "→" {
			t.Fatalf("el aviso no traía el comando nuevo: %v", values.Replacements)
		}
	default:
		t.Fatal("guardé los comandos y el daemon no se enteró")
	}
}

// Guardar el formulario de arriba no puede llevarse puestos los comandos: los
// dos escriben el mismo config y el daemon aplica el aviso tal cual viene.
func TestGuardarElFormularioNoBorraLosComandos(t *testing.T) {
	s, path := serverOn(t, "[commands]\nenabled = true\n\n[commands.replacements]\n\"flecha\" = \"→\"\n")

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/save", strings.NewReader(
		`{"engine":"faster-whisper","google_language":"es-AR","screen":"mouse",`+
			`"position":"bottom-center","commands":true,"trailing_space":true}`))
	s.handleSave(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("esperaba 200, vino %d: %s", rec.Code, rec.Body)
	}

	saved, err := config.Load(path)
	if err != nil {
		t.Fatalf("dejé el config roto: %v", err)
	}
	if saved.Commands.Replacements["flecha"] != "→" {
		t.Fatalf("el formulario se llevó puestos los comandos: %v", saved.Commands.Replacements)
	}
	select {
	case values := <-s.Saved():
		if values.Replacements["flecha"] != "→" {
			t.Fatalf("el aviso al daemon iba a borrarlos: %v", values.Replacements)
		}
	default:
		t.Fatal("el guardado no avisó nada")
	}
}

func TestCommandsRechazaLaFraseRepetida(t *testing.T) {
	s, _ := serverOn(t, "[commands]\nenabled = true\n")
	rec := post(t, s, `{"replacements":[
		{"say":"Punto Final","writes":"."},
		{"say":"punto final","writes":"!"}
	]}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("esperaba un 400 por la frase repetida, vino %d: %s", rec.Code, rec.Body)
	}
	if !strings.Contains(rec.Body.String(), "mismo comando") {
		t.Fatalf("el error tiene que decir qué pasó: %s", rec.Body)
	}
}

// La fila vacía del final es la que está para agregar y todavía no se llenó:
// no es un error, es que no escribiste nada.
func TestCommandsIgnoraLasFilasVacias(t *testing.T) {
	s, path := serverOn(t, "[commands]\nenabled = true\n")
	rec := post(t, s, `{"replacements":[
		{"say":"flecha","writes":"→"},
		{"say":"   ","writes":""},
		{"say":"","writes":"algo"}
	]}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("esperaba 200, vino %d: %s", rec.Code, rec.Body)
	}
	saved, err := config.Load(path)
	if err != nil {
		t.Fatalf("dejé el config roto: %v", err)
	}
	if len(saved.Commands.Replacements) != 1 {
		t.Fatalf("esperaba un solo comando, quedaron %v", saved.Commands.Replacements)
	}
}

func TestCommandsBorraLosQueYaNoEstan(t *testing.T) {
	s, path := serverOn(t, "[commands]\nenabled = true\n\n[commands.replacements]\n\"flecha\" = \"→\"\n\"arroba\" = \"@\"\n")
	if rec := post(t, s, `{"replacements":[{"say":"flecha","writes":"→"}]}`); rec.Code != http.StatusOK {
		t.Fatalf("esperaba 200, vino %d: %s", rec.Code, rec.Body)
	}
	saved, err := config.Load(path)
	if err != nil {
		t.Fatalf("dejé el config roto: %v", err)
	}
	if _, ok := saved.Commands.Replacements["arroba"]; ok {
		t.Fatalf("el que borré en la ventana sigue en el archivo: %v", saved.Commands.Replacements)
	}
}
