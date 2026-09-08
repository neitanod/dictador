package webconfig

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/neitanod/dictador/internal/config"
)

func serverConTraducción(t *testing.T, engine string) (*Server, string) {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")
	if err := os.WriteFile(path, []byte(config.Template), 0o644); err != nil {
		t.Fatalf("no pude escribir el config de prueba: %v", err)
	}
	cfg := config.Defaults()
	cfg.STT.Engine = engine
	cfg.Path = path
	s, err := New(cfg)
	if err != nil {
		t.Fatalf("no pude levantar el server: %v", err)
	}
	t.Cleanup(s.Close)
	return s, path
}

func TestLaPáginaMuestraLaTablaDeIdiomas(t *testing.T) {
	s, _ := serverConTraducción(t, "chrome")

	rec := httptest.NewRecorder()
	s.handlePage(rec, httptest.NewRequest(http.MethodGet, "/", nil))
	body := rec.Body.String()

	if rec.Code != http.StatusOK {
		t.Fatalf("esperaba 200, vino %d", rec.Code)
	}
	// El catálogo va como JSON de verdad adentro del script: si saliera como el
	// volcado de una estructura de Go, la página se rompería entera.
	if !strings.Contains(body, `{"code":"en","name":"Inglés"}`) {
		t.Error("el catálogo de idiomas no salió como JSON")
	}
	if !strings.Contains(body, `langRow("e", "en")`) {
		t.Error("faltó la fila de fábrica de la letra e")
	}
	if strings.Contains(body, "Anda con el motor") {
		t.Error("con el motor chrome no hay nada que advertir")
	}
}

// Con un motor que no traduce, la sección se muestra igual pero apagada y
// diciendo por qué: esconderla dejaría al que la busca creyendo que no existe.
func TestConOtroMotorLaTraducciónSeMuestraApagada(t *testing.T) {
	s, _ := serverConTraducción(t, "faster-whisper")

	rec := httptest.NewRecorder()
	s.handlePage(rec, httptest.NewRequest(http.MethodGet, "/", nil))
	body := rec.Body.String()

	if !strings.Contains(body, "Anda con el motor") {
		t.Error("faltó explicar por qué la traducción no está disponible")
	}
	if !strings.Contains(body, `id="translate"`) {
		t.Error("la sección tendría que estar igual, apagada")
	}
}

func guardar(t *testing.T, s *Server, values map[string]any) (int, map[string]any) {
	t.Helper()
	body, _ := json.Marshal(values)
	rec := httptest.NewRecorder()
	s.handleSave(rec, httptest.NewRequest(http.MethodPost, "/save", strings.NewReader(string(body))))
	var answer map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &answer)
	return rec.Code, answer
}

func TestGuardarLaTablaDeIdiomasLaEscribeEnElArchivo(t *testing.T) {
	s, path := serverConTraducción(t, "chrome")

	code, answer := guardar(t, s, map[string]any{
		"engine":         "chrome",
		"translate":      true,
		"translate_keys": map[string]string{"i": "it", "F": "FR"},
	})
	if code != http.StatusOK {
		t.Fatalf("esperaba 200, vino %d: %v", code, answer)
	}

	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("no pude leer el config: %v", err)
	}
	written := string(raw)
	for _, want := range []string{`"f" = "fr"`, `"i" = "it"`} {
		if !strings.Contains(written, want) {
			t.Errorf("faltó %s en el archivo:\n%s", want, written)
		}
	}
	// Y las de fábrica que sacaste tienen que irse de verdad: una tabla es una
	// lista, y guardarla es escribirla entera.
	if strings.Contains(written, `"e" = "en"`) {
		t.Error("la letra que borré volvió sola")
	}

	// Lo guardado se relee igual que se escribió.
	next, err := config.Load(path)
	if err != nil {
		t.Fatalf("el config quedó ilegible: %v", err)
	}
	if next.Translate.Keys["f"] != "fr" || len(next.Translate.Keys) != 2 {
		t.Errorf("releí %v", next.Translate.Keys)
	}
}

func TestLaMismaLetraDosVecesSeAvisa(t *testing.T) {
	s, _ := serverConTraducción(t, "chrome")

	// El mapa de JSON no puede tener la clave repetida, pero "E" y "e" son la
	// misma tecla y una de las dos se perdería en silencio.
	code, answer := guardar(t, s, map[string]any{
		"engine":         "chrome",
		"translate":      true,
		"translate_keys": map[string]string{"E": "en", "e": "pt"},
	})
	if code != http.StatusBadRequest {
		t.Fatalf("esperaba 400, vino %d: %v", code, answer)
	}
	if msg, _ := answer["error"].(string); !strings.Contains(msg, "dos veces") {
		t.Errorf("el mensaje no explica el choque: %q", msg)
	}
}

func TestApagarLaTraducciónSeGuarda(t *testing.T) {
	s, path := serverConTraducción(t, "chrome")

	if code, answer := guardar(t, s, map[string]any{
		"engine":         "chrome",
		"translate":      false,
		"translate_keys": map[string]string{"e": "en"},
	}); code != http.StatusOK {
		t.Fatalf("esperaba 200, vino %d: %v", code, answer)
	}

	next, err := config.Load(path)
	if err != nil {
		t.Fatalf("el config quedó ilegible: %v", err)
	}
	if next.Translate.Enabled {
		t.Error("la traducción tendría que haber quedado apagada")
	}
}

// El daemon se entera por el canal, y tiene que llegarle la tabla entera: un
// campo que viajara vacío le apagaría la traducción sin que nadie la tocara.
func TestGuardarLeAvisaAlDaemonConLaTablaEntera(t *testing.T) {
	s, _ := serverConTraducción(t, "chrome")

	guardar(t, s, map[string]any{
		"engine":         "chrome",
		"translate":      true,
		"translate_keys": map[string]string{"j": "ja"},
	})

	select {
	case values := <-s.Saved():
		if !values.Translate || values.TranslateKeys["j"] != "ja" {
			t.Fatalf("al daemon le llegó %+v", values)
		}
	default:
		t.Fatal("el daemon no se enteró de nada")
	}
}

// El modo se guarda y viaja al daemon: es la diferencia entre traducir por
// sentido y traducir palabra por palabra.
func TestElModoDeTraducciónSeGuarda(t *testing.T) {
	s, path := serverConTraducción(t, "chrome")

	if code, answer := guardar(t, s, map[string]any{
		"engine":         "chrome",
		"translate":      true,
		"translate_mode": "api",
		"translate_keys": map[string]string{"e": "en"},
	}); code != http.StatusOK {
		t.Fatalf("esperaba 200, vino %d: %v", code, answer)
	}

	next, err := config.Load(path)
	if err != nil {
		t.Fatalf("el config quedó ilegible: %v", err)
	}
	if next.Translate.Mode != "api" {
		t.Errorf("quedó en %q", next.Translate.Mode)
	}
	select {
	case values := <-s.Saved():
		if values.TranslateMode != "api" {
			t.Errorf("al daemon le llegó %q", values.TranslateMode)
		}
	default:
		t.Fatal("el daemon no se enteró")
	}
}

// Cualquier otra cosa cae en el modo que traduce mejor: un valor raro en el
// archivo no puede dejarte con la traducción literal sin que lo hayas pedido.
func TestUnModoRaroCaeEnElQueTraduceMejor(t *testing.T) {
	for _, raro := range []string{"", "  ", "página", "WEB"} {
		if got := translateMode(raro); got != "web" {
			t.Errorf("translateMode(%q) = %q", raro, got)
		}
	}
	if got := translateMode(" API "); got != "api" {
		t.Errorf("translateMode(\" API \") = %q", got)
	}
}
