// Package webconfig es la ventana de configuración, servida por el propio
// daemon en localhost.
//
// En la versión Python era un diálogo de Qt. Cambiarla por una página tiene un
// costo —aparece una pestaña en vez de un diálogo— y saca un toolkit gráfico
// entero del binario. Toca lo mismo que tocaba aquella: qué motor transcribe y
// lo que ese motor necesita para andar. El resto sigue en el config.toml, que
// tiene los comentarios explicando cada valor.
package webconfig

import (
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"net"
	"net/http"
	"os/exec"
	"strings"
	"sync"

	"github.com/neitanod/dictador/internal/commands"
	"github.com/neitanod/dictador/internal/config"
	"github.com/neitanod/dictador/internal/overlay"
	"github.com/neitanod/dictador/internal/stt"
	"github.com/neitanod/dictador/internal/x11"
)

//go:embed page.html
var pageFS embed.FS

// Values es lo que la página puede cambiar.
type Values struct {
	Engine         string `json:"engine"`
	GoogleAPIKey   string `json:"google_api_key"`
	GoogleLanguage string `json:"google_language"`
	ChromeLanguage string `json:"chrome_language"`
	Screen         string `json:"screen"`
	Position       string `json:"position"`
	Commands       bool   `json:"commands"`
}

// Server sirve la página y avisa cuando se guarda.
type Server struct {
	mu       sync.Mutex
	cfg      config.Config
	listener net.Listener
	server   *http.Server
	saved    chan Values
	quit     chan struct{}
	quitOnce sync.Once
	tmpl     *template.Template
}

// New levanta el server en un puerto al azar de loopback.
//
// El canal devuelve los valores nuevos cada vez que alguien guarda; el daemon
// lo escucha para cambiar el motor en caliente.
func New(cfg config.Config) (*Server, error) {
	tmpl, err := template.ParseFS(pageFS, "page.html")
	if err != nil {
		return nil, err
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, fmt.Errorf("no pude abrir la configuración web: %w", err)
	}
	s := &Server{
		cfg:      cfg,
		listener: listener,
		saved:    make(chan Values, 4),
		quit:     make(chan struct{}),
		tmpl:     tmpl,
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/", s.handlePage)
	mux.HandleFunc("/save", s.handleSave)
	mux.HandleFunc("/quit", s.handleQuit)
	s.server = &http.Server{Handler: mux}
	go func() { _ = s.server.Serve(listener) }()
	return s, nil
}

// URL es la dirección para abrir en el browser.
func (s *Server) URL() string {
	return "http://" + s.listener.Addr().String() + "/"
}

// Saved trae los valores cada vez que alguien guarda.
func (s *Server) Saved() <-chan Values { return s.saved }

// Quit se cierra cuando alguien apretó "Matar al dictador" en la página.
//
// Es un canal cerrado y no un valor porque la muerte pasa una sola vez y la
// tienen que ver todos los que estén mirando.
func (s *Server) Quit() <-chan struct{} { return s.quit }

// Update le cuenta al server que la configuración cambió por otro lado.
func (s *Server) Update(cfg config.Config) {
	s.mu.Lock()
	s.cfg = cfg
	s.mu.Unlock()
}

// Close cierra el server.
func (s *Server) Close() {
	if s.server != nil {
		_ = s.server.Close()
	}
}

// Open abre la configuración en el browser del sistema.
//
// Si hay Chrome va como ventana de app —sin barra de direcciones ni pestañas—,
// que se parece bastante al diálogo que esto era en la versión Python. Lo que
// decide es el botón que mata al dictador: una ventana de app se puede cerrar
// por script y una pestaña común no, así que el "también la página" sólo se
// cumple de verdad acá.
func (s *Server) Open() error {
	var last error
	for _, cmd := range s.openCommands() {
		if err := cmd.Start(); err != nil {
			last = err
			continue
		}
		// El Chrome que ya estaba corriendo se queda con la ventana y este
		// proceso termina enseguida: hay que juntarlo o queda de zombi.
		go func() { _ = cmd.Wait() }()
		return nil
	}
	if last == nil {
		last = errors.New("no encontré con qué abrir el browser")
	}
	return last
}

// chromeQuiet son los flags que hacen que Chrome abra la página y nada más.
//
// Sin ellos, un Chrome que arranca con un perfil recién nacido muestra primero
// el diálogo de bienvenida —"Make Google Chrome the default browser" y las
// estadísticas de uso— y la ventana que le pedimos ni aparece hasta que alguien
// le da OK. Con el perfil de todos los días eso ya está contestado y no se ve,
// así que el diálogo salta justo cuando molesta más: probando con un HOME o un
// XDG_CONFIG_HOME temporal, donde el perfil siempre es virgen y el que se come
// la ventana en la cara es el que está usando la máquina.
// El otro que se cuela es el globito de Google Translate, y ese no se apaga
// desde acá: lo apaga el notranslate de page.html. Probé con
// --disable-features=Translate y la burbuja aparecía igual.
var chromeQuiet = []string{
	"--no-first-run",
	"--no-default-browser-check",
	"--disable-extensions",
}

// openCommands son las maneras de abrir la página, de la que mejor queda a la
// que anda en cualquier lado.
func (s *Server) openCommands() []*exec.Cmd {
	s.mu.Lock()
	preferred := s.cfg.STT.ChromeBinary
	s.mu.Unlock()

	var cmds []*exec.Cmd
	if chrome := stt.ChromeBinary(preferred); chrome != "" {
		args := append(append([]string{}, chromeQuiet...), "--app="+s.URL())
		cmds = append(cmds, exec.Command(chrome, args...))
	}
	return append(cmds, exec.Command("xdg-open", s.URL()))
}

// option es una opción de un select: lo que se guarda y lo que se lee.
type option struct {
	Value    string
	Label    string
	Selected bool
}

// view es lo que la plantilla necesita saber.
type view struct {
	Engine         string
	WhisperOK      bool
	WhisperDetail  string
	ChromeOK       bool
	ChromeDetail   string
	GoogleAPIKey   string
	Language       string
	KeyFromEnv     bool
	HotkeyLabel    string
	ConfigPath     string
	WhisperCommand string
	Screens        []option
	Positions      []option
	Monitors       []x11.Monitor
	Commands       bool
	CommandCount   int
}

func (s *Server) snapshot() view {
	s.mu.Lock()
	cfg := s.cfg
	s.mu.Unlock()

	opts := stt.OptionsFrom(cfg)
	engine, err := stt.Canonical(cfg.STT.Engine)
	if err != nil {
		engine = "whisper"
	}
	whisperOK := stt.WhisperAvailable(opts)
	chromeBinary := stt.ChromeBinary(cfg.STT.ChromeBinary)

	v := view{
		Engine:      engine,
		WhisperOK:   whisperOK,
		ChromeOK:    chromeBinary != "",
		Language:    stt.GoogleLocale(cfg.STT),
		HotkeyLabel: cfg.Hotkey.Key,
		ConfigPath:  cfg.Path,
		WhisperCommand: "whisper-server -m models/ggml-" + orElse(cfg.STT.Model, "small") +
			".bin --host 127.0.0.1 --port 8080",
		Commands:     cfg.Commands.Enabled,
		CommandCount: len(commands.List(commands.OptionsFrom(cfg))),
	}
	if whisperOK {
		v.WhisperDetail = "hay un whisper-server contestando en " + cfg.STT.WhisperServerURL
	} else {
		v.WhisperDetail = "no hay ningún whisper-server en " + cfg.STT.WhisperServerURL
	}
	if v.ChromeOK {
		v.ChromeDetail = chromeBinary
	} else {
		v.ChromeDetail = "no encontré Chrome en el PATH; instalá google-chrome para tener este motor"
	}
	// La key del entorno gana sobre la del archivo, y hay que decirlo antes de
	// que alguien deje el campo vacío y crea que la borró.
	v.KeyFromEnv = stt.GoogleAPIKey(config.STT{}) != ""
	if !v.KeyFromEnv {
		v.GoogleAPIKey = cfg.STT.GoogleAPIKey
	}
	if v.ConfigPath == "" {
		v.ConfigPath = config.ConfigPath()
	}

	v.Monitors = monitors()
	// Las pantallas conectadas van como opciones más de la lista: elegir una por
	// nombre es lo que quiere decir "siempre en la misma".
	for _, s := range overlay.Screens {
		v.Screens = append(v.Screens, option{
			Value: s.Value, Label: s.Label, Selected: cfg.Overlay.Screen == s.Value,
		})
	}
	for _, m := range v.Monitors {
		if m.Name == "" {
			continue
		}
		label := "siempre en " + m.Name
		if m.Primary {
			label += " (principal)"
		}
		v.Screens = append(v.Screens, option{
			Value: m.Name, Label: label, Selected: cfg.Overlay.Screen == m.Name,
		})
	}
	for _, p := range overlay.Positions {
		v.Positions = append(v.Positions, option{
			Value: p.Value, Label: p.Label, Selected: cfg.Overlay.Position == p.Value,
		})
	}
	if !anySelected(v.Screens) && len(v.Screens) > 0 {
		v.Screens[0].Selected = true
	}
	if !anySelected(v.Positions) {
		for i := range v.Positions {
			v.Positions[i].Selected = v.Positions[i].Value == "bottom-center"
		}
	}
	return v
}

func anySelected(options []option) bool {
	for _, o := range options {
		if o.Selected {
			return true
		}
	}
	return false
}

// monitors lista las pantallas conectadas, o nada si no hay display.
func monitors() []x11.Monitor {
	conn, err := x11.Open()
	if err != nil {
		return nil
	}
	defer conn.Close()
	return conn.Monitors()
}

func orElse(value, fallback string) string {
	if value == "" {
		return fallback
	}
	return value
}

func (s *Server) handlePage(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := s.tmpl.Execute(w, s.snapshot()); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

func (s *Server) handleSave(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "sólo POST", http.StatusMethodNotAllowed)
		return
	}
	var values Values
	if err := json.NewDecoder(r.Body).Decode(&values); err != nil {
		http.Error(w, "no entendí el pedido", http.StatusBadRequest)
		return
	}
	engine, err := stt.Canonical(values.Engine)
	if err != nil {
		replyJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	// El idioma es uno solo en la página y se guarda para los dos motores por
	// internet: son la misma pregunta hecha una vez.
	locale := strings.TrimSpace(values.GoogleLanguage)

	s.mu.Lock()
	cfg := s.cfg
	s.mu.Unlock()

	if engine == "google" && strings.TrimSpace(values.GoogleAPIKey) == "" &&
		stt.GoogleAPIKey(config.STT{}) == "" {
		replyJSON(w, http.StatusBadRequest, map[string]string{
			"error": "Sin API key Google no puede transcribir. Pegala o elegí otro motor.",
		})
		return
	}

	// El nombre que va al archivo es el que el config entiende, no el interno.
	stored := engine
	if engine == "whisper" {
		stored = "faster-whisper"
	}
	screen := strings.TrimSpace(values.Screen)
	if screen == "" {
		screen = "mouse"
	}
	position := strings.TrimSpace(values.Position)
	if !overlay.ValidPosition(position) {
		position = "bottom-center"
	}

	settings := []config.Setting{
		{Section: "stt", Key: "engine", Value: stored},
		{Section: "stt", Key: "google_language", Value: locale},
		{Section: "stt", Key: "chrome_language", Value: locale},
		{Section: "overlay", Key: "screen", Value: screen},
		{Section: "overlay", Key: "position", Value: position},
		{Section: "commands", Key: "enabled", Value: values.Commands},
	}
	if !s.snapshot().KeyFromEnv {
		settings = append(settings,
			config.Setting{Section: "stt", Key: "google_api_key",
				Value: strings.TrimSpace(values.GoogleAPIKey)})
	}
	path := cfg.Path
	if path == "" {
		path = config.ConfigPath()
	}
	if _, err := config.Save(path, settings); err != nil {
		replyJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}

	cfg.STT.Engine = stored
	cfg.STT.GoogleLanguage = locale
	cfg.STT.ChromeLanguage = locale
	cfg.Overlay.Screen = screen
	cfg.Overlay.Position = position
	cfg.Commands.Enabled = values.Commands
	if !s.snapshot().KeyFromEnv {
		cfg.STT.GoogleAPIKey = strings.TrimSpace(values.GoogleAPIKey)
	}
	cfg.Path = path
	s.mu.Lock()
	s.cfg = cfg
	s.mu.Unlock()

	out := Values{
		Engine:         stored,
		GoogleAPIKey:   cfg.STT.GoogleAPIKey,
		GoogleLanguage: locale,
		ChromeLanguage: locale,
		Screen:         screen,
		Position:       position,
		Commands:       values.Commands,
	}
	select {
	case s.saved <- out:
	default:
	}
	replyJSON(w, http.StatusOK, map[string]any{"ok": true, "engine": stored, "path": path})
}

// handleQuit contesta primero y avisa después: en cuanto el daemon se entere va
// a cerrar este mismo server, y una respuesta a medio salir dejaría a la página
// esperando a un programa que ya no está.
func (s *Server) handleQuit(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "sólo POST", http.StatusMethodNotAllowed)
		return
	}
	replyJSON(w, http.StatusOK, map[string]any{"ok": true})
	if flusher, ok := w.(http.Flusher); ok {
		flusher.Flush()
	}
	s.quitOnce.Do(func() { close(s.quit) })
}

func replyJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}
