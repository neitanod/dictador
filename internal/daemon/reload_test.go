package daemon

import (
	"slices"
	"strings"
	"testing"

	"github.com/neitanod/dictador/internal/config"
)

func TestGuardarLoMismoNoMueveNada(t *testing.T) {
	cfg := config.Defaults()
	plan := planReload(cfg, cfg)
	if plan.busy() {
		t.Errorf("un archivo que dice lo mismo movió algo: %+v", plan)
	}
}

func TestCambiarElMotorLoRearma(t *testing.T) {
	old := config.Defaults()
	next := old
	next.STT.Engine = "chrome"

	plan := planReload(old, next)
	if !plan.Engine {
		t.Error("cambió el motor en el archivo y el daemon lo dejó como estaba")
	}
	if len(plan.Restart) != 0 {
		t.Errorf("el motor se cambia en caliente, no pide reiniciar: %v", plan.Restart)
	}
}

func TestCambiarUnaVueltaDeTuercaDelMotorTambiénLoRearma(t *testing.T) {
	old := config.Defaults()
	next := old
	// El modelo, el idioma o la URL del whisper-server son el motor igual que el
	// nombre del motor: si cambian, lo que está andando ya no es lo que pediste.
	next.STT.Model = "large-v3"

	if !planReload(old, next).Engine {
		t.Error("cambió el modelo y el motor siguió con el viejo")
	}
}

func TestMoverLaVentanitaNoRearmaElMotor(t *testing.T) {
	old := config.Defaults()
	next := old
	next.Overlay.Position = "top-center"

	plan := planReload(old, next)
	if !plan.Placement {
		t.Error("la ventanita tenía que mudarse")
	}
	if plan.Engine {
		t.Error("mover la ventanita rearmó el motor al pedo")
	}
}

func TestLoQueSeLeeEnCalienteNoPideNada(t *testing.T) {
	old := config.Defaults()
	next := old
	next.Commands.Enabled = !old.Commands.Enabled
	next.Action.TrailingSpace = !old.Action.TrailingSpace
	next.Limits.MaxSeconds = old.Limits.MaxSeconds + 10
	next.Hotkey.Mode = "toggle"

	plan := planReload(old, next)
	if plan.busy() {
		t.Errorf("esto se lee en cada dictado y no necesita nada: %+v", plan)
	}
}

func TestLaTeclaNuevaPideReiniciar(t *testing.T) {
	old := config.Defaults()
	next := old
	next.Hotkey.Key = "Super_L"

	plan := planReload(old, next)
	if !slices.ContainsFunc(plan.Restart, func(qué string) bool {
		return strings.Contains(qué, "tecla")
	}) {
		t.Errorf("la tecla se agarra al arrancar y hay que decirlo: %v", plan.Restart)
	}
}

func TestElMicrófonoYLaVentanitaApagadaPidenReiniciar(t *testing.T) {
	old := config.Defaults()
	next := old
	next.Audio.Device = "alsa_input.usb"
	next.Overlay.Enabled = !old.Overlay.Enabled

	plan := planReload(old, next)
	if len(plan.Restart) != 2 {
		t.Errorf("esperaba dos avisos de reinicio, vinieron %v", plan.Restart)
	}
}

func TestElAvisoDeReinicioSeLee(t *testing.T) {
	plan := reloadPlan{Restart: []string{"la tecla", "el micrófono"}}
	// El texto va a la ventanita, así que tiene que decir qué pasó y qué falta
	// hacer sin obligar a ir a leer el log.
	aviso := plan.notice()
	for _, parte := range []string{"la tecla", "el micrófono", "reinici"} {
		if !strings.Contains(aviso, parte) {
			t.Errorf("el aviso %q no dice %q", aviso, parte)
		}
	}
	if (reloadPlan{}).notice() != "" {
		t.Error("sin nada que avisar, no se molesta a nadie")
	}
}

// Cambiar de dónde sale la traducción rearma el motor: el modo se decide
// cuando el motor se construye, y sin rearmarlo seguirías traduciendo por el
// camino de antes.
func TestCambiarElModoDeTraducciónRearmaElMotor(t *testing.T) {
	old := config.Defaults()
	next := old
	next.Translate.Mode = "api"

	if plan := planReload(old, next); !plan.Engine {
		t.Errorf("el plan quedó %+v", plan)
	}
}
