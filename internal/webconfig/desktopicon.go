package webconfig

// El ícono del escritorio, desde la página.
//
// La página lo pregunta y lo pide por fetch en vez de recibirlo dibujado en el
// HTML, y eso es a propósito: instalar el ícono cambia lo que el botón tiene
// que decir —"agregar" pasa a "sacar"— y con la respuesta del propio pedido se
// redibuja solo, sin recargar una página donde uno puede estar a mitad de
// escribir la API key.

import (
	"encoding/json"
	"net/http"

	"github.com/neitanod/dictador/internal/desktop"
)

// handleIcon sirve el mismo dibujo que se instala en el escritorio, para que la
// página lo muestre arriba del título y lo use de favicon.
//
// Es el favicon el que obliga a que esto sea una dirección y no un SVG pegado
// dentro del HTML: el browser lo pide aparte. Y en la ventana que abre `dictador
// config` —un Chrome en modo --app, sin barra de direcciones— ese favicon es
// además el ícono que se ve en la barra de tareas mientras la configuración
// está abierta.
func (s *Server) handleIcon(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "image/svg+xml")
	// El server escucha en un puerto distinto cada vez que abrís la
	// configuración, así que una copia guardada no se reusaría casi nunca y sí
	// podría mostrar el ícono viejo después de actualizar el programa.
	w.Header().Set("Cache-Control", "no-store")
	_, _ = w.Write(desktop.IconSVG())
}

// iconActions es lo que la página puede hacerle al escritorio.
type iconActions struct {
	install       func() (desktop.Result, error)
	remove        func() (desktop.Result, error)
	status        func() desktop.State
	autostart     func(bool) error
	autostartOn   func() bool
	autostartPath func() string
}

// realIcon es el que toca el escritorio de verdad.
var realIcon = iconActions{
	install: func() (desktop.Result, error) { return desktop.Install(desktop.Binary()) },
	remove:  desktop.Uninstall,
	status:  desktop.Status,
	autostart: func(on bool) error {
		if !on {
			return desktop.RemoveAutostart()
		}
		_, err := desktop.InstallAutostart(desktop.Binary())
		return err
	},
	autostartOn:   desktop.AutostartInstalled,
	autostartPath: desktop.AutostartPath,
}

// iconReply es lo que la página necesita para dibujar el botón.
type iconReply struct {
	OK        bool   `json:"ok"`
	Installed bool   `json:"installed"`
	Autostart bool   `json:"autostart"`
	Desktop   string `json:"desktop,omitempty"`
	Shortcut  string `json:"shortcut,omitempty"`
	Note      string `json:"note,omitempty"`
	Error     string `json:"error,omitempty"`
}

// autostartOn es "¿arranca solo?", con la respuesta prudente cuando la prueba
// no puso a nadie que conteste.
func (s *Server) autostartOn() bool {
	if s.icon.autostartOn == nil {
		return false
	}
	return s.icon.autostartOn()
}

// handleDesktopIcon cuenta si el ícono está puesto (GET) y lo pone o lo saca
// (POST). Los dos contestan lo mismo, así que la página tiene un solo lugar
// donde decidir qué mostrar.
func (s *Server) handleDesktopIcon(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		state := s.icon.status()
		replyJSON(w, http.StatusOK, iconReply{
			OK: true, Installed: state.Installed, Autostart: s.autostartOn(),
			Desktop: state.Desktop, Shortcut: state.Shortcut,
		})

	case http.MethodPost:
		var body struct {
			Action string `json:"action"`
		}
		// Un body vacío es "poné el ícono": es lo que se pide casi siempre y no
		// tiene sentido que falle por una llave que falta.
		_ = json.NewDecoder(r.Body).Decode(&body)
		// El arranque automático es otra pregunta que la misma sección contesta:
		// una cosa es tener el ícono para prenderlo cuando querés, y otra que se
		// prenda solo. Comparten endpoint porque comparten el archivo.
		if body.Action == "autostart-on" || body.Action == "autostart-off" {
			on := body.Action == "autostart-on"
			if err := s.icon.autostart(on); err != nil {
				replyJSON(w, http.StatusInternalServerError, iconReply{
					Installed: s.icon.status().Installed,
					Autostart: s.autostartOn(), Error: err.Error()})
				return
			}
			state := s.icon.status()
			replyJSON(w, http.StatusOK, iconReply{OK: true,
				Installed: state.Installed, Shortcut: state.Shortcut,
				Desktop: state.Desktop, Autostart: on})
			return
		}
		if body.Action == "remove" {
			result, err := s.icon.remove()
			if err != nil {
				replyJSON(w, http.StatusInternalServerError, iconReply{
					Installed: true, Desktop: result.Desktop, Error: err.Error()})
				return
			}
			replyJSON(w, http.StatusOK, iconReply{OK: true,
				Autostart: s.autostartOn(), Desktop: result.Desktop})
			return
		}
		result, err := s.icon.install()
		if err != nil {
			replyJSON(w, http.StatusInternalServerError, iconReply{
				Desktop: result.Desktop, Error: err.Error()})
			return
		}
		reply := iconReply{
			OK: true, Installed: true, Autostart: s.autostartOn(),
			Desktop: result.Desktop, Shortcut: result.Shortcut,
		}
		if len(result.Notes) > 0 {
			reply.Note = result.Notes[0]
		}
		replyJSON(w, http.StatusOK, reply)

	default:
		http.Error(w, "sólo GET o POST", http.StatusMethodNotAllowed)
	}
}
