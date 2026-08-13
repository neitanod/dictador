package daemon

import (
	"testing"

	"github.com/neitanod/dictador/internal/config"
)

func TestPostprocessNormalizaLosEspacios(t *testing.T) {
	got := Postprocess("  hola   mundo \n del dictador ", config.Action{})
	if got != "hola mundo del dictador" {
		t.Errorf("quedó %q", got)
	}
}

func TestPostprocessSacaElPuntoFinalSiSeLoPiden(t *testing.T) {
	action := config.Action{StripFinalPeriod: true}
	if got := Postprocess("hola mundo.", action); got != "hola mundo" {
		t.Errorf("quedó %q", got)
	}
	// Sólo el último, y sólo si es un punto.
	if got := Postprocess("hola mundo...", action); got != "hola mundo.." {
		t.Errorf("quedó %q", got)
	}
	if got := Postprocess("¿hola?", action); got != "¿hola?" {
		t.Errorf("quedó %q", got)
	}
}

func TestPostprocessAgregaElEspacioDeAtras(t *testing.T) {
	action := config.Action{TrailingSpace: true}
	if got := Postprocess("hola", action); got != "hola " {
		t.Errorf("quedó %q", got)
	}
	// Sobre un texto vacío no agrega nada: un espacio suelto no es un dictado.
	if got := Postprocess("   ", action); got != "" {
		t.Errorf("quedó %q", got)
	}
}

func TestPostprocessCombinaLasDos(t *testing.T) {
	action := config.Action{StripFinalPeriod: true, TrailingSpace: true}
	if got := Postprocess("  hola  mundo. ", action); got != "hola mundo " {
		t.Errorf("quedó %q", got)
	}
}

func TestActionLabelsCubreLasCuatroAcciones(t *testing.T) {
	for _, action := range []string{"paste", "type", "clipboard", "keep_open"} {
		if actionLabels[action] == "" {
			t.Errorf("falta cómo contarle al usuario la acción %q", action)
		}
	}
}

func TestEstadosSeNombranParaElLog(t *testing.T) {
	for state, want := range map[state]string{
		idle: "idle", armed: "armed", recording: "recording", thinking: "thinking",
	} {
		if got := state.String(); got != want {
			t.Errorf("%d → %q, quería %q", state, got, want)
		}
	}
}

func TestElPreviewMuestraLosComandosYaAplicados(t *testing.T) {
	cfg := config.Defaults()
	got := Preview("abre pregunta cómo andás signo de pregunta", cfg)
	if got != "¿cómo andás?" {
		t.Errorf("quedó %q", got)
	}
	// Y lo que borra, borrado: si no, mientras hablás ves texto que ya no va.
	if got := Preview("esto está mal borrá eso esto está bien", cfg); got != "esto está bien" {
		t.Errorf("quedó %q", got)
	}
}

func TestElPreviewDejaElPuntoFinalQueEstáPorCrecer(t *testing.T) {
	cfg := config.Defaults()
	cfg.Action.StripFinalPeriod = true
	cfg.Action.TrailingSpace = true
	// El parcial se está escribiendo: recortarle el punto de ahora sería
	// recortar el medio de una oración.
	if got := Preview("hola mundo.", cfg); got != "hola mundo." {
		t.Errorf("quedó %q", got)
	}
}

func TestConLosComandosApagadosElPreviewEsElTextoCrudo(t *testing.T) {
	cfg := config.Defaults()
	cfg.Commands.Enabled = false
	if got := Preview("hola coma qué tal", cfg); got != "hola coma qué tal" {
		t.Errorf("quedó %q", got)
	}
}
