package webconfig

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/neitanod/dictador/internal/config"
	"github.com/neitanod/dictador/internal/desktop"
)

// iconServer arma un server con el escritorio de mentira: las pruebas miran qué
// se le pidió sin que aparezca un lanzador en el escritorio del que las corre.
func iconServer(t *testing.T, actions iconActions) *Server {
	t.Helper()
	s, err := New(config.Config{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.Close)
	s.icon = actions
	return s
}

func iconRequest(t *testing.T, s *Server, method, body string) (int, iconReply) {
	t.Helper()
	var reader *strings.Reader
	if body == "" {
		reader = strings.NewReader("")
	} else {
		reader = strings.NewReader(body)
	}
	request := httptest.NewRequest(method, "/desktop-icon", reader)
	recorder := httptest.NewRecorder()
	s.handleDesktopIcon(recorder, request)
	var reply iconReply
	if err := json.Unmarshal(recorder.Body.Bytes(), &reply); err != nil {
		t.Fatalf("no es JSON: %v\n%s", err, recorder.Body.String())
	}
	return recorder.Code, reply
}

func TestDesktopIconCuentaSiEstaPuesto(t *testing.T) {
	s := iconServer(t, iconActions{
		status: func() desktop.State {
			return desktop.State{Installed: true, Desktop: "KDE Plasma",
				Shortcut: "/home/quien/Desktop/dictador.desktop"}
		},
	})
	code, reply := iconRequest(t, s, http.MethodGet, "")
	if code != http.StatusOK || !reply.Installed {
		t.Fatalf("code=%d reply=%+v", code, reply)
	}
	if reply.Desktop != "KDE Plasma" || reply.Shortcut == "" {
		t.Errorf("la página no se entera de dónde está: %+v", reply)
	}
}

func TestDesktopIconInstalaConBodyVacio(t *testing.T) {
	var asked int
	s := iconServer(t, iconActions{
		install: func() (desktop.Result, error) {
			asked++
			return desktop.Result{Desktop: "GNOME",
				Shortcut: "/home/quien/Escritorio/dictador.desktop",
				Notes:    []string{"ojo con esto"}}, nil
		},
	})
	code, reply := iconRequest(t, s, http.MethodPost, "")
	if code != http.StatusOK || asked != 1 {
		t.Fatalf("code=%d instalaciones=%d", code, asked)
	}
	if !reply.Installed || reply.Note != "ojo con esto" {
		t.Errorf("reply = %+v", reply)
	}
}

func TestDesktopIconSaca(t *testing.T) {
	var removed bool
	s := iconServer(t, iconActions{
		remove: func() (desktop.Result, error) {
			removed = true
			return desktop.Result{Desktop: "Xfce"}, nil
		},
	})
	code, reply := iconRequest(t, s, http.MethodPost, `{"action":"remove"}`)
	if code != http.StatusOK || !removed {
		t.Fatalf("code=%d removed=%v", code, removed)
	}
	if reply.Installed {
		t.Error("después de sacarlo no tendría que quedar instalado")
	}
}

func TestDesktopIconCuentaElErrorConPalabras(t *testing.T) {
	s := iconServer(t, iconActions{
		install: func() (desktop.Result, error) {
			return desktop.Result{}, errors.New("no encontré la carpeta de tu escritorio")
		},
	})
	code, reply := iconRequest(t, s, http.MethodPost, "{}")
	if code != http.StatusInternalServerError {
		t.Fatalf("code = %d", code)
	}
	if !strings.Contains(reply.Error, "carpeta de tu escritorio") {
		t.Errorf("el error no llega a la página: %+v", reply)
	}
}

func TestDesktopIconRechazaOtrosMetodos(t *testing.T) {
	s := iconServer(t, iconActions{})
	request := httptest.NewRequest(http.MethodDelete, "/desktop-icon", nil)
	recorder := httptest.NewRecorder()
	s.handleDesktopIcon(recorder, request)
	if recorder.Code != http.StatusMethodNotAllowed {
		t.Fatalf("code = %d", recorder.Code)
	}
}

// La página tiene que traer el botón: es lo único que pidió el usuario, y un
// endpoint sin botón no lo cumple.
func TestLaPaginaTraeElBotonDelIcono(t *testing.T) {
	s := iconServer(t, iconActions{})
	request := httptest.NewRequest(http.MethodGet, "/", nil)
	recorder := httptest.NewRecorder()
	s.handlePage(recorder, request)
	body := recorder.Body.String()
	for _, want := range []string{"desktopIcon", "Agregar ícono al escritorio", "/desktop-icon"} {
		if !strings.Contains(body, want) {
			t.Errorf("falta %q en la página", want)
		}
	}
}

func TestDesktopIconPrendeYApagaElArranqueAutomatico(t *testing.T) {
	on := false
	actions := iconActions{
		status:      func() desktop.State { return desktop.State{Installed: true} },
		autostartOn: func() bool { return on },
		autostart: func(wanted bool) error {
			on = wanted
			return nil
		},
	}
	s := iconServer(t, actions)

	code, reply := iconRequest(t, s, http.MethodPost, `{"action":"autostart-on"}`)
	if code != http.StatusOK || !reply.Autostart || !on {
		t.Fatalf("code=%d reply=%+v on=%v", code, reply, on)
	}
	code, reply = iconRequest(t, s, http.MethodPost, `{"action":"autostart-off"}`)
	if code != http.StatusOK || reply.Autostart || on {
		t.Fatalf("code=%d reply=%+v on=%v", code, reply, on)
	}
	// Y el estado lo cuenta sin que haya que preguntarlo aparte.
	on = true
	if _, reply := iconRequest(t, s, http.MethodGet, ""); !reply.Autostart {
		t.Errorf("el GET no cuenta que arranca solo: %+v", reply)
	}
}

func TestDesktopIconCuentaSiElArranqueAutomaticoFalla(t *testing.T) {
	s := iconServer(t, iconActions{
		status:      func() desktop.State { return desktop.State{} },
		autostartOn: func() bool { return false },
		autostart:   func(bool) error { return errors.New("el disco está lleno") },
	})
	code, reply := iconRequest(t, s, http.MethodPost, `{"action":"autostart-on"}`)
	if code != http.StatusInternalServerError || !strings.Contains(reply.Error, "disco") {
		t.Fatalf("code=%d reply=%+v", code, reply)
	}
}
