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
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/neitanod/dictador/internal/commands"
	"github.com/neitanod/dictador/internal/config"
	"github.com/neitanod/dictador/internal/overlay"
	"github.com/neitanod/dictador/internal/stt"
	"github.com/neitanod/dictador/internal/translate"
	"github.com/neitanod/dictador/internal/x11"
)

//go:embed page.html
var pageFS embed.FS

// Values es lo que la página puede cambiar.
//
// Va entero en cada aviso, incluso lo que el formulario que guardó no tocó:
// del otro lado se aplica tal cual, y un campo que viajara vacío borraría lo
// que el otro formulario acababa de guardar.
type Values struct {
	Engine         string `json:"engine"`
	GoogleAPIKey   string `json:"google_api_key"`
	GoogleLanguage string `json:"google_language"`
	ChromeLanguage string `json:"chrome_language"`
	Screen         string `json:"screen"`
	Position       string `json:"position"`
	Commands       bool   `json:"commands"`
	TrailingSpace  bool   `json:"trailing_space"`
	// Replacements son los comandos hablados que el usuario cambió, agregó o
	// apagó, indexados por la frase tal como la dice.
	Replacements map[string]string `json:"replacements"`
	// Translate prende la traducción instantánea, y TranslateKeys es la tabla
	// letra → idioma con la que se elige a cuál.
	Translate     bool              `json:"translate"`
	TranslateKeys map[string]string `json:"translate_keys"`
	// TranslateMode es de dónde sale la traducción: "web" o "api".
	TranslateMode string `json:"translate_mode"`
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
	// edit abre el config.toml en un editor. Es un campo y no una llamada
	// directa para que las pruebas puedan pedirlo sin que se abra un editor de
	// verdad en la máquina del que las corre.
	edit func(path string) error
	// icon pone y saca el ícono del escritorio, y es un campo por lo mismo:
	// las pruebas lo piden sin llenarle el escritorio de lanzadores al que las
	// corre. Vive en desktopicon.go.
	icon iconActions
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
		edit:     openEditor,
		icon:     realIcon,
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/", s.handlePage)
	mux.HandleFunc("/save", s.handleSave)
	mux.HandleFunc("/commands", s.handleCommands)
	mux.HandleFunc("/edit", s.handleEdit)
	mux.HandleFunc("/desktop-icon", s.handleDesktopIcon)
	mux.HandleFunc("/icon.svg", s.handleIcon)
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

// guiEditors son editores de ventana, del que viene con el escritorio al que
// alguien instaló a propósito.
var guiEditors = []string{
	"gnome-text-editor", "gedit", "kate", "kwrite", "mousepad", "xed",
	"pluma", "leafpad", "geany", "code", "subl",
}

// consoleOnly son editores que sólo existen adentro de una terminal: lanzarlos
// sueltos desde acá los mata sin que nadie vea nada.
var consoleOnly = []string{
	"vi", "vim", "nvim", "nano", "emacs", "helix", "hx", "micro", "joe",
	"mcedit", "ne", "kak", "pico",
}

// terminals son las terminales que aceptan -e para correr algo adentro. La
// lista es corta a propósito: las que no lo aceptan —kitty, wezterm— piden cada
// una su propia sintaxis, y este es el último recurso, no el camino principal.
var terminals = []string{
	"x-terminal-emulator", "gnome-terminal", "konsole", "xfce4-terminal",
	"mate-terminal", "alacritty", "xterm",
}

// consoleEditors es lo que se abre en la terminal si nadie dijo cuál quiere.
var consoleEditors = []string{"nano", "vim", "vi"}

// editCommands son las maneras de abrir el config.toml, de la que el usuario
// eligió a la que anda en cualquier lado.
//
// Primero va lo que haya en VISUAL o EDITOR, que es el usuario diciendo con qué
// edita y no deja nada que adivinar. Después los editores de texto de ventana
// que estén instalados. xdg-open, que parece el candidato natural, quedó
// anteúltimo: abre con lo que el escritorio tenga asociado a la extensión, y en
// una Ubuntu de todos los días un .toml lo abre LibreOffice Writer, que además
// de tardar una eternidad ofrece guardarlo como .odt. De última, el editor de
// consola adentro de una terminal, que es lo único que hay en una máquina sin
// escritorio.
func editCommands(path string) []*exec.Cmd {
	var cmds []*exec.Cmd
	if chosen := chosenEditor(); chosen != "" {
		cmds = append(cmds, editorCommand(chosen, path))
	}
	for _, editor := range guiEditors {
		if found, err := exec.LookPath(editor); err == nil {
			cmds = append(cmds, exec.Command(found, path))
		}
	}
	cmds = append(cmds, exec.Command("xdg-open", path))
	if editor := consoleEditor(); editor != "" {
		cmds = append(cmds, terminalCommand(editor, path))
	}
	return cmds
}

// chosenEditor es lo que el usuario puso en VISUAL o EDITOR.
func chosenEditor() string {
	for _, name := range []string{os.Getenv("VISUAL"), os.Getenv("EDITOR")} {
		if strings.TrimSpace(name) != "" {
			return strings.TrimSpace(name)
		}
	}
	return ""
}

// consoleEditor es el editor de consola del usuario, o el primero que haya.
func consoleEditor() string {
	if chosen := chosenEditor(); chosen != "" {
		return chosen
	}
	for _, name := range consoleEditors {
		if _, err := exec.LookPath(name); err == nil {
			return name
		}
	}
	return ""
}

// editorCommand arma la corrida del editor elegido: si es de los que viven en
// una terminal, adentro de una; si no, suelto.
func editorCommand(editor, path string) *exec.Cmd {
	fields := strings.Fields(editor)
	if len(fields) == 0 {
		return exec.Command("xdg-open", path)
	}
	if slices.Contains(consoleOnly, filepath.Base(fields[0])) {
		return terminalCommand(editor, path)
	}
	return exec.Command(fields[0], append(fields[1:], path)...)
}

// terminalCommand abre el editor adentro de una terminal.
func terminalCommand(editor, path string) *exec.Cmd {
	args := append([]string{"-e"}, strings.Fields(editor)...)
	return exec.Command(terminalBinary(), append(args, path)...)
}

// terminalBinary es la terminal donde meter al editor de consola. Si no hay
// ninguna se devuelve igual la de Debian: el intento falla y no cambia nada,
// pero la lista de intentos no se queda coja según en qué máquina corra.
func terminalBinary() string {
	for _, name := range terminals {
		if found, err := exec.LookPath(name); err == nil {
			return found
		}
	}
	return "x-terminal-emulator"
}

// editorGrace es lo que se espera a ver si el editor se murió apenas arrancó.
//
// Sin esta espera, un editor que no está andando cuenta como éxito igual: el
// que no encuentra display arranca bien y recién después se cae. Un editor que
// abrió de verdad no termina en este rato, así que seguir vivo es la señal de
// que anduvo. Segundo y pico porque los pesados —kate, code— tardan bastante
// más que eso en darse cuenta de que no pueden abrir una ventana.
var editorGrace = 1200 * time.Millisecond

// openEditor abre el archivo con lo primero que funcione.
func openEditor(path string) error {
	var last error
	for _, cmd := range editCommands(path) {
		if err := startEditor(cmd); err != nil {
			last = err
			continue
		}
		return nil
	}
	if last == nil {
		last = errors.New("no encontré con qué abrir el config.toml")
	}
	return last
}

func startEditor(cmd *exec.Cmd) error {
	if err := cmd.Start(); err != nil {
		return err
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	select {
	case err := <-done:
		return err
	case <-time.After(editorGrace):
		return nil
	}
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
	TrailingSpace  bool
	Translate      bool
	TranslateWeb   bool
	TranslateKeys  []translate.Binding
	Languages      []translate.Language
	// CanTranslate es si el motor elegido sabe traducir. Con los otros la
	// sección se muestra igual, apagada y diciendo por qué.
	CanTranslate bool
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
		Commands:      cfg.Commands.Enabled,
		CommandCount:  len(commands.List(commands.OptionsFrom(cfg))),
		TrailingSpace: cfg.Action.TrailingSpace,
		Translate:     cfg.Translate.Enabled,
		TranslateWeb:  !strings.EqualFold(strings.TrimSpace(cfg.Translate.Mode), "api"),
		TranslateKeys: translate.Bindings(cfg.Translate.Keys),
		Languages:     translate.Languages,
		CanTranslate:  stt.TranslatorEngines[engine],
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
	// Las letras de la traducción se revisan contra el teclado de verdad: una
	// que este teclado no tiene se guardaría igual y no dispararía nunca, y el
	// que la escribió creería que la traducción está rota.
	keys, err := cleanTranslateKeys(values.TranslateKeys)
	if err != nil {
		replyJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
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
		{Section: "action", Key: "trailing_space", Value: values.TrailingSpace},
		{Section: "translate", Key: "enabled", Value: values.Translate},
		{Section: "translate", Key: "mode", Value: translateMode(values.TranslateMode)},
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
	// La tabla va aparte porque es una lista: [Save] sabe cambiar valores y no
	// sabe sacar la letra que borraste.
	if _, err := config.SaveTable(path, "translate.keys", translatePairs(keys)); err != nil {
		replyJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}

	cfg.STT.Engine = stored
	cfg.STT.GoogleLanguage = locale
	cfg.STT.ChromeLanguage = locale
	cfg.Overlay.Screen = screen
	cfg.Overlay.Position = position
	cfg.Commands.Enabled = values.Commands
	cfg.Action.TrailingSpace = values.TrailingSpace
	cfg.Translate.Enabled = values.Translate
	cfg.Translate.Mode = translateMode(values.TranslateMode)
	cfg.Translate.Keys = keys
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
		TrailingSpace:  values.TrailingSpace,
		Replacements:   cfg.Commands.Replacements,
		Translate:      values.Translate,
		TranslateMode:  translateMode(values.TranslateMode),
		TranslateKeys:  keys,
	}
	s.notify(out)
	replyJSON(w, http.StatusOK, map[string]any{"ok": true, "engine": stored, "path": path})
}

// commandsRequest es la tabla de comandos entera tal como quedó en la ventana.
//
// Viaja como lista y no como objeto para poder cazar la frase repetida: dos
// claves iguales en un objeto JSON se pisan sin que nadie se entere, y quien
// la escribió merece que se lo digan.
type commandsRequest struct {
	Replacements []commandEdit `json:"replacements"`
}

type commandEdit struct {
	Say    string `json:"say"`
	Writes string `json:"writes"`
}

// handleCommands lista los comandos y guarda los que cambiaste.
//
// Lo que se guarda es sólo lo que difiere de fábrica: un comando que no tocaste
// no se escribe en el config, así que el día que cambie el catálogo tu archivo
// no lo deja clavado en la versión vieja.
func (s *Server) handleCommands(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		s.mu.Lock()
		cfg := s.cfg
		s.mu.Unlock()
		replyJSON(w, http.StatusOK, map[string]any{
			"entries": commands.Entries(commands.OptionsFrom(cfg)),
			"enabled": cfg.Commands.Enabled,
		})
	case http.MethodPost:
		s.saveCommands(w, r)
	default:
		http.Error(w, "sólo GET o POST", http.StatusMethodNotAllowed)
	}
}

func (s *Server) saveCommands(w http.ResponseWriter, r *http.Request) {
	var req commandsRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "no entendí el pedido", http.StatusBadRequest)
		return
	}
	pairs, replacements, err := cleanCommands(req.Replacements)
	if err != nil {
		replyJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}

	s.mu.Lock()
	cfg := s.cfg
	s.mu.Unlock()
	path := cfg.Path
	if path == "" {
		path = config.ConfigPath()
	}
	if _, err := config.SaveTable(path, "commands.replacements", pairs); err != nil {
		replyJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}

	cfg.Commands.Replacements = replacements
	cfg.Path = path
	s.mu.Lock()
	s.cfg = cfg
	s.mu.Unlock()

	s.notify(Values{
		Engine:         cfg.STT.Engine,
		GoogleAPIKey:   cfg.STT.GoogleAPIKey,
		GoogleLanguage: cfg.STT.GoogleLanguage,
		ChromeLanguage: cfg.STT.ChromeLanguage,
		Screen:         cfg.Overlay.Screen,
		Position:       cfg.Overlay.Position,
		Commands:       cfg.Commands.Enabled,
		TrailingSpace:  cfg.Action.TrailingSpace,
		Replacements:   replacements,
		Translate:      cfg.Translate.Enabled,
		TranslateMode:  cfg.Translate.Mode,
		TranslateKeys:  cfg.Translate.Keys,
	})
	replyJSON(w, http.StatusOK, map[string]any{
		"ok":      true,
		"path":    path,
		"entries": commands.Entries(commands.OptionsFrom(cfg)),
	})
}

// cleanCommands revisa la tabla que llegó y la deja lista para el archivo.
//
// Las frases vacías se tiran sin decir nada: son la fila para agregar que
// quedó sin llenar. La repetida sí se avisa, porque una de las dos se iba a
// perder en silencio.
func cleanCommands(edits []commandEdit) ([]config.Pair, map[string]string, error) {
	pairs := make([]config.Pair, 0, len(edits))
	replacements := make(map[string]string, len(edits))
	seen := make(map[string]string, len(edits))
	for _, e := range edits {
		say := strings.TrimSpace(e.Say)
		key := commands.NormalizePhrase(say)
		if key == "" {
			continue
		}
		if before, repeated := seen[key]; repeated {
			return nil, nil, fmt.Errorf("%q y %q son el mismo comando: dejá uno solo", before, say)
		}
		seen[key] = say
		pairs = append(pairs, config.Pair{Key: say, Value: e.Writes})
		replacements[say] = e.Writes
	}
	sort.Slice(pairs, func(i, j int) bool {
		return commands.NormalizePhrase(pairs[i].Key) < commands.NormalizePhrase(pairs[j].Key)
	})
	return pairs, replacements, nil
}

// translateMode deja el modo en uno de los dos que existen. Cualquier otra cosa
// cae en "web", que es el que traduce mejor.
func translateMode(mode string) string {
	if strings.EqualFold(strings.TrimSpace(mode), "api") {
		return "api"
	}
	return "web"
}

// cleanTranslateKeys revisa la tabla de la traducción y la deja lista para el
// archivo.
//
// Se tiran las filas a medio llenar, se avisa la letra repetida —porque una de
// las dos se perdería en silencio— y se avisa la que este teclado no tiene.
func cleanTranslateKeys(table map[string]string) (map[string]string, error) {
	keys := make(map[string]string, len(table))
	seen := make(map[string]bool, len(table))
	for raw, language := range table {
		key := translate.NormalizeKey(raw)
		language = translate.NormalizeCode(language)
		if key == "" || language == "" {
			continue
		}
		if seen[key] {
			return nil, fmt.Errorf("la tecla %q está dos veces: dejá una sola", key)
		}
		seen[key] = true
		keys[key] = language
	}
	if missing := unknownKeys(keys); len(missing) > 0 {
		return nil, fmt.Errorf("tu teclado no tiene la tecla %q", missing[0])
	}
	return keys, nil
}

// unknownKeys son las letras que el teclado de esta máquina no tiene.
//
// Sin display no se puede saber, y ahí se dan todas por buenas: es lo que pasa
// en los tests y en una sesión sin X, y negarse a guardar por no poder mirar
// sería peor que guardar de más.
func unknownKeys(keys map[string]string) []string {
	conn, err := x11.Open()
	if err != nil {
		return nil
	}
	defer conn.Close()
	keymap, err := conn.LoadKeymap()
	if err != nil {
		return nil
	}
	var missing []string
	for key := range keys {
		if _, err := keymap.KeycodesFor(key); err != nil {
			missing = append(missing, key)
		}
	}
	sort.Strings(missing)
	return missing
}

// translatePairs ordena la tabla como se escribe en el archivo.
func translatePairs(keys map[string]string) []config.Pair {
	bindings := translate.Bindings(keys)
	pairs := make([]config.Pair, 0, len(bindings))
	for _, b := range bindings {
		pairs = append(pairs, config.Pair{Key: b.Key, Value: b.Language})
	}
	return pairs
}

// notify le pasa al daemon los valores nuevos, si es que hay quien escuche.
func (s *Server) notify(values Values) {
	select {
	case s.saved <- values:
	default:
	}
}

// handleEdit abre el config.toml en un editor de la máquina.
//
// Lo que la página cambia es un puñado de valores; el resto vive en el archivo,
// con los comentarios que explican cada uno. Mostrar la ruta obligaba a copiarla
// y salir a buscar el archivo a mano, y este link se saltea ese paso.
func (s *Server) handleEdit(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "sólo POST", http.StatusMethodNotAllowed)
		return
	}
	s.mu.Lock()
	path := s.cfg.Path
	s.mu.Unlock()
	if path == "" {
		path = config.ConfigPath()
	}
	// Nadie tocó nunca el config y el archivo puede no existir todavía: abrirlo
	// así sería una hoja en blanco, sin los comentarios que dicen qué se puede
	// cambiar. Se escribe la plantilla y recién entonces se abre.
	if err := ensureConfigFile(path); err != nil {
		replyJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	if err := s.edit(path); err != nil {
		replyJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	replyJSON(w, http.StatusOK, map[string]any{"ok": true, "path": path})
}

func ensureConfigFile(path string) error {
	if _, err := os.Stat(path); err == nil {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, []byte(config.Template), 0o644)
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
