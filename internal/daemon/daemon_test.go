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
