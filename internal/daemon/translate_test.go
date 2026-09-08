package daemon

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/neitanod/dictador/internal/commands"
	"github.com/neitanod/dictador/internal/config"
	"github.com/neitanod/dictador/internal/stt"
)

// espía es la ventanita de mentira: se queda con lo último que le dijeron.
type espía struct {
	label string
	text  string
	hint  string
	hide  time.Duration
}

func (e *espía) BeginListening(hint string) { e.label, e.hint = hint, hint }
func (e *espía) SetPartial(text string)     { e.text = text }
func (e *espía) SetHint(hint string)        { e.hint, e.label = hint, hint }
func (e *espía) SetMeter(float64, float64)  {}
func (e *espía) SetThinking(status string)  { e.label = status }
func (e *espía) SetDone(text, status string, hideAfter time.Duration) {
	e.text, e.label, e.hide = text, status, hideAfter
}
func (e *espía) SetError(message string) { e.label = message }
func (e *espía) Dismiss()                {}
func (e *espía) Close()                  {}

// portapapeles de mentira, para ver qué queda a mano después de cancelar.
type portapapeles struct{ text string }

func (c *portapapeles) Set(text string) error { c.text = text; return nil }
func (c *portapapeles) Close()                {}

type pegado struct {
	plan   commands.Plan
	action string
	veces  int
	err    error
}

func daemonDePrueba(t *testing.T, cfg config.Config) (*Daemon, *espía, *portapapeles, *pegado) {
	t.Helper()
	// El historial se escribe en disco: que sea uno de este test y no el de la
	// máquina del que lo corre.
	t.Setenv("XDG_STATE_HOME", t.TempDir())

	ui, clip, puesto := &espía{}, &portapapeles{}, &pegado{}
	d := &Daemon{
		cfg:     cfg,
		ui:      ui,
		clip:    clip,
		results: make(chan result, 8),
		quit:    make(chan struct{}),
		log:     func(string) {},
	}
	d.put = func(plan commands.Plan, action string) error {
		puesto.plan, puesto.action, puesto.veces = plan, action, puesto.veces+1
		return puesto.err
	}
	return d, ui, clip, puesto
}

func cfgTraducción() config.Config {
	cfg := config.Defaults()
	cfg.Action.OnRelease = "paste"
	cfg.Overlay.HideDelayMs = 1400
	return cfg
}

// Un dictado traducido no se pega de una: se muestra el ratito que pediste,
// con el texto ya traducido a la vista y Escape a mano.
func TestLaTraducciónSeMuestraAntesDePegarse(t *testing.T) {
	d, ui, _, puesto := daemonDePrueba(t, cfgTraducción())

	d.deliver(result{text: "Hello there", language: "en"})

	if d.state != previewing {
		t.Fatalf("quedó en %s, quería previewing", d.state)
	}
	if puesto.veces != 0 {
		t.Error("no se tenía que pegar nada todavía")
	}
	if ui.text != "Hello there" {
		t.Errorf("la ventanita muestra %q", ui.text)
	}
	if ui.label == "" || ui.hide != 0 {
		t.Errorf("la ventanita tiene que quedarse mientras dure la pausa: %q, %v", ui.label, ui.hide)
	}
}

func TestCuandoSeAcabaLaPausaElTextoSePega(t *testing.T) {
	d, ui, _, puesto := daemonDePrueba(t, cfgTraducción())
	d.deliver(result{text: "Hello there", language: "en"})

	d.commitPreview()

	if d.state != idle {
		t.Fatalf("quedó en %s, quería idle", d.state)
	}
	if puesto.veces != 1 || puesto.plan.Text() != "Hello there" {
		t.Fatalf("se pegó %d veces: %q", puesto.veces, puesto.plan.Text())
	}
	if ui.label != "Pegado en inglés" {
		t.Errorf("la ventanita dice %q", ui.label)
	}
}

// Escape es el segundo que pediste para arrepentirte: no se pega nada, y la
// traducción queda en el clipboard por si igual la querías.
func TestEscapeCancelaElPegadoYDejaElTextoAMano(t *testing.T) {
	d, ui, clip, puesto := daemonDePrueba(t, cfgTraducción())
	d.deliver(result{text: "Hello there", language: "en"})

	d.onEscape()

	if d.state != idle {
		t.Fatalf("quedó en %s, quería idle", d.state)
	}
	if puesto.veces != 0 {
		t.Error("se pegó igual después de cancelar")
	}
	if clip.text != "Hello there" {
		t.Errorf("el clipboard quedó con %q", clip.text)
	}
	if ui.label == "" {
		t.Error("la ventanita tendría que decir que se canceló")
	}
}

// Volver a apretar la tecla del dictado con una traducción esperando es darla
// por buena: se pega y se sigue.
func TestApretarLaTeclaDeNuevoDaLaTraducciónPorBuena(t *testing.T) {
	d, _, _, puesto := daemonDePrueba(t, cfgTraducción())
	d.deliver(result{text: "Hello there", language: "en"})

	d.onPress() // sin micrófono no llega a grabar, y no es lo que se prueba acá

	if puesto.veces != 1 {
		t.Fatalf("se pegó %d veces, quería una", puesto.veces)
	}
}

// Sin traducción, el dictado se pega en el acto: la pausa es del texto que
// pasó por el traductor y de ningún otro.
func TestElDictadoSinTraducirSePegaDerecho(t *testing.T) {
	d, _, _, puesto := daemonDePrueba(t, cfgTraducción())

	d.deliver(result{text: "hola mundo"})

	if d.state == previewing {
		t.Fatal("un dictado sin traducir no tiene que esperar nada")
	}
	if puesto.veces != 1 || puesto.plan.Text() != "hola mundo" {
		t.Fatalf("se pegó %d veces: %q", puesto.veces, puesto.plan.Text())
	}
}

// Con preview_ms en cero se pega derecho, que es para el que la pausa le
// molesta más de lo que le sirve.
func TestConLaPausaEnCeroSePegaDerecho(t *testing.T) {
	cfg := cfgTraducción()
	cfg.Translate.PreviewMs = 0
	d, _, _, puesto := daemonDePrueba(t, cfg)

	d.deliver(result{text: "Hello there", language: "en"})

	if d.state == previewing || puesto.veces != 1 {
		t.Fatalf("quedó en %s y se pegó %d veces", d.state, puesto.veces)
	}
}

// Un traductor que no contesta no puede costarte el dictado: va lo que
// dijiste, y la ventanita cuenta por qué.
func TestSiLaTraducciónFallaSePegaElOriginalConElAviso(t *testing.T) {
	d, ui, _, puesto := daemonDePrueba(t, cfgTraducción())

	d.deliver(result{text: "hola mundo", warn: "sin traducir: no hay internet"})

	if puesto.veces != 1 || puesto.plan.Text() != "hola mundo" {
		t.Fatalf("se pegó %d veces: %q", puesto.veces, puesto.plan.Text())
	}
	if ui.label != "Pegado · sin traducir: no hay internet" {
		t.Errorf("la ventanita dice %q", ui.label)
	}
}

// Lo que queda en el clipboard no se pega en ninguna ventana, así que no hay
// pegado que frenar y la pausa sobra.
func TestLoQueVaAlClipboardNoEsperaNada(t *testing.T) {
	cfg := cfgTraducción()
	cfg.Action.OnRelease = "clipboard"
	d, _, _, puesto := daemonDePrueba(t, cfg)

	d.deliver(result{text: "Hello there", language: "en"})

	if d.state == previewing || puesto.veces != 1 {
		t.Fatalf("quedó en %s y se pegó %d veces", d.state, puesto.veces)
	}
}

// Los comandos hablados ya corrieron antes de traducir: volver a pasarlos por
// el texto en inglés convertiría un "comma" en un signo que nadie pidió.
func TestElTextoTraducidoNoVuelveAPasarPorLosComandos(t *testing.T) {
	cfg := cfgTraducción()
	cfg.Translate.PreviewMs = 0
	d, _, _, puesto := daemonDePrueba(t, cfg)

	d.deliver(result{text: "comma and enter", language: "en"})

	if got := puesto.plan.Text(); got != "comma and enter" {
		t.Fatalf("quedó %q", got)
	}
	if puesto.plan.HasKeys() {
		t.Error("el texto traducido no tendría que traer teclas adentro")
	}
}

func TestElEspacioDeAtrásTambiénValeParaLaTraducción(t *testing.T) {
	cfg := cfgTraducción()
	cfg.Translate.PreviewMs = 0
	cfg.Action.TrailingSpace = true
	cfg.Action.StripFinalPeriod = true
	d, _, _, puesto := daemonDePrueba(t, cfg)

	d.deliver(result{text: "Hello there.", language: "en"})

	if got := puesto.plan.Text(); got != "Hello there " {
		t.Fatalf("quedó %q", got)
	}
}

// Si el pegado falla, el texto tiene que quedar en algún lado igual.
func TestUnPegadoQueFallaDejaElTextoEnElClipboard(t *testing.T) {
	d, ui, clip, puesto := daemonDePrueba(t, cfgTraducción())
	puesto.err = errors.New("la ventana se fue")

	d.deliver(result{text: "hola mundo"})

	if clip.text != "hola mundo" {
		t.Errorf("el clipboard quedó con %q", clip.text)
	}
	if ui.label == "Pegado" {
		t.Error("la ventanita tendría que decir que no se pudo pegar")
	}
}

func TestLaEsperaSeLeeComoSeDice(t *testing.T) {
	casos := []struct {
		delay time.Duration
		want  string
	}{
		{time.Second, "1 s"},
		{1200 * time.Millisecond, "1,2 s"},
		{2 * time.Second, "2 s"},
	}
	for _, c := range casos {
		if got := formatDelay(c.delay); got != c.want {
			t.Errorf("formatDelay(%v) = %q, quería %q", c.delay, got, c.want)
		}
	}
}

// motorDeMentira transcribe lo que le digan y traduce anotando qué le
// mandaron: lo que se prueba es qué texto llega al traductor.
type motorDeMentira struct {
	dice      string
	recibió   string
	traduce   string
	falla     error
	traducido bool
}

func (m *motorDeMentira) Name() string          { return "mentira" }
func (m *motorDeMentira) Describe() string      { return "motor de mentira" }
func (m *motorDeMentira) Load() error           { return nil }
func (m *motorDeMentira) SupportsPartial() bool { return false }
func (m *motorDeMentira) Close()                {}
func (m *motorDeMentira) Transcribe([]float32, bool) (string, error) {
	return m.dice, nil
}
func (m *motorDeMentira) Translate(text, target string) (stt.Translation, error) {
	m.recibió, m.traducido = text, true
	if m.falla != nil {
		return stt.Translation{}, m.falla
	}
	return stt.Translation{Text: m.traduce, From: "es"}, nil
}

// Decir "coma" escribe una coma, y eso pasa antes de traducir: si el traductor
// recibiera la palabra, del otro lado saldría "comma" escrito con letras.
func TestLosComandosHabladosCorrenAntesDeTraducir(t *testing.T) {
	motor := &motorDeMentira{dice: "hola coma cómo andás", traduce: "hi, how are you"}

	res := dictate(motor, nil, true, "en", config.Defaults(), func(string) {})

	if motor.recibió != "hola, cómo andás" {
		t.Fatalf("al traductor le llegó %q", motor.recibió)
	}
	if len(res) != 2 || res[0].stage != "translating" {
		t.Fatalf("los avisos salieron %+v", res)
	}
	last := res[len(res)-1]
	if last.text != "hi, how are you" || last.language != "en" {
		t.Fatalf("el resultado quedó %+v", last)
	}
}

func TestSinIdiomaNoSeLlamaAlTraductor(t *testing.T) {
	motor := &motorDeMentira{dice: "hola mundo"}

	res := dictate(motor, nil, true, "", config.Defaults(), func(string) {})

	if motor.traducido {
		t.Error("se llamó al traductor sin que nadie lo pidiera")
	}
	if len(res) != 1 || res[0].text != "hola mundo" {
		t.Fatalf("el resultado quedó %+v", res)
	}
}

func TestUnTraductorCaídoDevuelveElDictadoConElAviso(t *testing.T) {
	motor := &motorDeMentira{dice: "hola mundo", falla: errors.New("no hay internet")}

	res := dictate(motor, nil, true, "en", config.Defaults(), func(string) {})

	last := res[len(res)-1]
	if last.text != "hola mundo" || last.language != "" {
		t.Fatalf("el resultado quedó %+v", last)
	}
	if !strings.Contains(last.warn, "no hay internet") {
		t.Errorf("el aviso quedó %q", last.warn)
	}
}

// Un dictado que quedó en nada no se manda a traducir: sería un viaje a
// internet para traducir el vacío.
func TestUnDictadoVacíoNoSeTraduce(t *testing.T) {
	motor := &motorDeMentira{dice: "   "}

	dictate(motor, nil, true, "en", config.Defaults(), func(string) {})

	if motor.traducido {
		t.Error("se mandó a traducir un dictado vacío")
	}
}
