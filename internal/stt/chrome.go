package stt

import (
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"
)

// El motor Chrome usa la Web Speech API, la misma que el microfonito de
// google.com. Google tiene dos servicios de voz con el mismo apellido: Cloud
// Speech-to-Text es el producto comercial, con API key y factura por minuto; la
// Web Speech API es la que Chrome le da gratis a las páginas, y no se puede
// llamar desde afuera del browser porque las claves van compiladas adentro de
// Chrome. Este motor la usa desde adentro: un Chrome headless residente con una
// página local servida por la propia app.
//
// El reconocimiento corre sólo entre el press y el release de la tecla. El
// proceso de Chrome queda vivo, pero con el micrófono cerrado.

// chromeBinaries: google-chrome primero, porque los builds de Chromium suelen
// venir sin las claves de Google y sin ellas el reconocimiento no responde.
var chromeBinaries = []string{"google-chrome", "google-chrome-stable", "chromium", "chromium-browser"}

// chromeErrors traduce lo que devuelve la Web Speech API a algo accionable.
var chromeErrors = map[string]string{
	"network":             "Chrome no pudo hablar con el servicio de voz de Google (¿hay internet?)",
	"not-allowed":         "Chrome no le dio permiso al micrófono",
	"service-not-allowed": "este Chrome no tiene habilitado el servicio de voz de Google",
	"audio-capture":       "Chrome no pudo abrir el micrófono",
	"aborted":             "el reconocimiento se cortó antes de tiempo",
	"sin-web-speech":      "este navegador no tiene la Web Speech API (¿es Chromium sin claves?)",
}

// chromeSilent: que no hayas dicho nada no es un error, es un dictado vacío.
var chromeSilent = map[string]bool{"no-speech": true}

const chromePage = `<!doctype html>
<meta charset="utf-8">
<title>dictador</title>
<body>
<script>
const LANG = %s;
const post = (kind, text) =>
  fetch('/event', {method: 'POST', body: JSON.stringify({kind, text: text || ''})})
    .catch(() => {});

let rec = null, heard = '';

function start() {
  if (rec) return;
  rec = new webkitSpeechRecognition();
  rec.lang = LANG;
  // continuous para que no corte sola en la primera pausa, e interimResults
  // para dibujar el texto en vivo mientras hablás.
  rec.continuous = true;
  rec.interimResults = true;
  heard = '';
  rec.onresult = e => {
    // e.results es acumulativo: se rearma entero en cada evento.
    let out = '';
    for (const r of e.results) out += r[0].transcript;
    heard = out.trim();
    post('partial', heard);
  };
  rec.onerror = e => post('error', e.error);
  rec.onend = () => { const text = heard; rec = null; post('final', text); };
  rec.start();
}

function stop() {
  if (rec) rec.stop();       // el final llega por onend
  else post('final', '');
}

async function loop() {
  for (;;) {
    try {
      const answer = await fetch('/command');
      const command = (await answer.text()).trim();
      if (command === 'start') start();
      else if (command === 'stop') stop();
    } catch (e) {
      await new Promise(r => setTimeout(r, 500));
    }
  }
}

if (!('webkitSpeechRecognition' in window)) post('error', 'sin-web-speech');
post('ready', '');
loop();
</script>
</body>
`

// ChromeBinary busca el ejecutable de Chrome.
func ChromeBinary(preferred string) string {
	if preferred != "" {
		if path, err := lookPath(preferred); err == nil {
			return path
		}
		if _, err := os.Stat(preferred); err == nil {
			return preferred
		}
		return ""
	}
	for _, name := range chromeBinaries {
		if path, err := lookPath(name); err == nil {
			return path
		}
	}
	return ""
}

// ChromeAvailable dice si esta máquina puede usar el motor Chrome.
func ChromeAvailable(preferred string) bool { return ChromeBinary(preferred) != "" }

// Chrome es el motor vivo: escucha él mismo, en vez de recibir el audio.
type Chrome struct {
	binary       string
	language     string
	source       string // PULSE_SOURCE, que es como se le elige el micrófono
	readyTimeout time.Duration
	finalTimeout time.Duration
	headless     bool

	commands chan string

	mu        sync.Mutex
	proc      *exec.Cmd
	server    *http.Server
	listener  net.Listener
	profile   string
	text      string
	final     string
	failure   string
	listening bool
	exited    chan struct{}

	ready chan struct{}
	done  chan struct{}
	// readyOnce y doneOnce evitan cerrar dos veces el mismo canal cuando la
	// página manda un evento repetido.
	readyOnce sync.Once
}

// NewChrome arma el motor. No lanza nada hasta Load.
func NewChrome(opts Options) *Chrome {
	language := strings.TrimSpace(opts.STT.ChromeLanguage)
	if language == "" {
		language = GoogleLocale(opts.STT)
	}
	return &Chrome{
		binary:       ChromeBinary(opts.STT.ChromeBinary),
		language:     language,
		source:       opts.Device,
		readyTimeout: seconds(opts.STT.ChromeReadyTimeoutS, 25),
		finalTimeout: seconds(opts.STT.ChromeFinalTimeoutS, 6),
		headless:     opts.STT.ChromeHeadless,
		commands:     make(chan string, 4),
		ready:        make(chan struct{}),
		done:         make(chan struct{}),
	}
}

func seconds(value, fallback float64) time.Duration {
	if value <= 0 {
		value = fallback
	}
	return time.Duration(value * float64(time.Second))
}

func (c *Chrome) Name() string          { return "chrome" }
func (c *Chrome) SupportsPartial() bool { return true }
func (c *Chrome) Describe() string      { return fmt.Sprintf("Chrome / Web Speech (%s)", c.language) }

// page es el HTML que Chrome va a correr, con el idioma ya adentro.
func (c *Chrome) page() string {
	lang, _ := json.Marshal(c.language)
	return fmt.Sprintf(chromePage, lang)
}

func closed(ch chan struct{}) bool {
	select {
	case <-ch:
		return true
	default:
		return false
	}
}

// Load levanta el server local y el Chrome, y espera a que la página salude.
func (c *Chrome) Load() error {
	c.mu.Lock()
	if c.binary == "" {
		c.mu.Unlock()
		return errf("no encontré Chrome en el PATH; instalalo o elegí otro motor")
	}
	if c.server == nil {
		if err := c.startServer(); err != nil {
			c.mu.Unlock()
			return err
		}
	}
	needsLaunch := c.proc == nil || closed(c.exited)
	if needsLaunch {
		c.ready = make(chan struct{})
		c.readyOnce = sync.Once{}
		if err := c.launch(); err != nil {
			c.mu.Unlock()
			return err
		}
	}
	ready, exited := c.ready, c.exited
	c.mu.Unlock()

	select {
	case <-ready:
	case <-exited:
		return errf("Chrome se cerró solo apenas arrancó")
	case <-time.After(c.readyTimeout):
		return errf("Chrome no levantó la página de dictado en %.0fs", c.readyTimeout.Seconds())
	}

	c.mu.Lock()
	failure := c.failure
	c.mu.Unlock()
	if failure != "" {
		return errf("%s", explain(failure))
	}
	return nil
}

func explain(code string) string {
	if msg, ok := chromeErrors[code]; ok {
		return msg
	}
	return code
}

// startServer abre el HTTP local en un puerto al azar de loopback.
func (c *Chrome) startServer() error {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return errf("no pude abrir el server local para Chrome: %v", err)
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte(c.page()))
	})
	// Long polling: la página pregunta y se queda esperando. Así el «empezá»
	// llega en el mismo instante en que apretás la tecla.
	mux.HandleFunc("/command", func(w http.ResponseWriter, r *http.Request) {
		select {
		case cmd := <-c.commands:
			_, _ = w.Write([]byte(cmd))
		case <-time.After(20 * time.Second):
			_, _ = w.Write([]byte("noop"))
		case <-r.Context().Done():
		}
	})
	mux.HandleFunc("/event", func(w http.ResponseWriter, r *http.Request) {
		var payload struct {
			Kind string `json:"kind"`
			Text string `json:"text"`
		}
		_ = json.NewDecoder(r.Body).Decode(&payload)
		c.onEvent(payload.Kind, payload.Text)
		w.WriteHeader(http.StatusOK)
	})
	c.listener = listener
	c.server = &http.Server{Handler: mux}
	go func() { _ = c.server.Serve(listener) }()
	return nil
}

// launch abre el Chrome apuntando a la página local.
func (c *Chrome) launch() error {
	// Perfil propio y descartable. Con uno compartido, un Chrome que todavía lo
	// tiene tomado hace que el nuevo le pase el pedido al viejo y se cierre solo
	// — y entonces la página de dictado nunca aparece.
	profile, err := os.MkdirTemp("", "dictador-chrome-")
	if err != nil {
		return err
	}
	c.profile = profile

	args := []string{
		"--disable-gpu",
		"--no-first-run",
		"--no-default-browser-check",
		"--disable-extensions",
		"--user-data-dir=" + profile,
		// El permiso de micrófono se autoacepta: la página es nuestra y no hay
		// nadie para clickear el diálogo.
		"--use-fake-ui-for-media-stream",
		fmt.Sprintf("http://127.0.0.1:%d/", c.listener.Addr().(*net.TCPAddr).Port),
	}
	if c.headless {
		args = append([]string{"--headless=new"}, args...)
	}
	cmd := exec.Command(c.binary, args...)
	cmd.Env = os.Environ()
	if c.source != "" {
		// Chrome agarra la entrada por default del sistema y no le importan los
		// flags de micrófono falso. Se lo apunta con PULSE_SOURCE, que además es
		// lo que hace que respete el [audio] device de la app. Sin esto, en la
		// primera prueba Chrome escuchó la sala y le mandó a Google lo que
		// sonaba en los parlantes.
		cmd.Env = append(cmd.Env, "PULSE_SOURCE="+c.source)
	}
	cmd.Stdout, cmd.Stderr = nil, nil
	if err := cmd.Start(); err != nil {
		return errf("no pude arrancar Chrome: %v", err)
	}
	exited := make(chan struct{})
	c.proc, c.exited = cmd, exited
	go func() { _ = cmd.Wait(); close(exited) }()
	return nil
}

// onEvent recibe lo que la página cuenta: ready, partial, final o error.
func (c *Chrome) onEvent(kind, text string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	switch kind {
	case "ready":
		c.readyOnce.Do(func() { close(c.ready) })
	case "partial":
		c.text = text
	case "final":
		c.final = text
		c.signalDone()
	case "error":
		if chromeSilent[text] {
			c.signalDone()
			return
		}
		c.failure = text
		// Para que Load no se quede esperando a una página que ya falló.
		c.readyOnce.Do(func() { close(c.ready) })
		c.signalDone()
	}
}

func (c *Chrome) signalDone() {
	select {
	case <-c.done:
	default:
		close(c.done)
	}
}

// StartLive: el daemon avisa que empezó a grabar, Chrome abre el micrófono.
func (c *Chrome) StartLive() error {
	if err := c.Load(); err != nil {
		return err
	}
	c.mu.Lock()
	c.text, c.final, c.failure = "", "", ""
	c.done = make(chan struct{})
	c.listening = true
	c.mu.Unlock()
	c.send("start")
	return nil
}

// AbortLive: dictado cancelado, cerrar el micrófono y tirar lo que se oyó.
func (c *Chrome) AbortLive() {
	c.mu.Lock()
	if !c.listening {
		c.mu.Unlock()
		return
	}
	c.listening = false
	c.mu.Unlock()

	c.send("stop")
	c.mu.Lock()
	c.text, c.final = "", ""
	c.mu.Unlock()
}

// PartialText es lo que Chrome lleva escuchado.
func (c *Chrome) PartialText() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.text
}

// FinishLive cierra el micrófono y devuelve el texto definitivo.
func (c *Chrome) FinishLive() (string, error) {
	c.mu.Lock()
	if !c.listening {
		c.mu.Unlock()
		return "", nil
	}
	c.listening = false
	done := c.done
	c.mu.Unlock()

	c.send("stop")
	gotFinal := true
	select {
	case <-done:
	case <-time.After(c.finalTimeout):
		gotFinal = false
	}

	c.mu.Lock()
	failure, final, partial := c.failure, c.final, c.text
	c.mu.Unlock()

	if failure != "" {
		return "", errf("%s", explain(failure))
	}
	if !gotFinal && partial == "" {
		return "", errf("Chrome no devolvió el texto a tiempo")
	}
	// Si el final tardó más de la cuenta, el último parcial es mejor que nada.
	if final != "" {
		return final, nil
	}
	return partial, nil
}

func (c *Chrome) send(command string) {
	select {
	case c.commands <- command:
	case <-time.After(2 * time.Second):
	}
}

// Transcribe existe para cumplir la interfaz: el audio que grabó el daemon no
// se usa, lo que transcribe es lo que Chrome escuchó por su cuenta.
func (c *Chrome) Transcribe(_ []float32, partial bool) (string, error) {
	if partial {
		return c.PartialText(), nil
	}
	return c.FinishLive()
}

// Close cierra Chrome, el server y el perfil temporal.
func (c *Chrome) Close() {
	c.mu.Lock()
	proc, server, listener, profile, exited := c.proc, c.server, c.listener, c.profile, c.exited
	c.proc, c.server, c.listener, c.profile = nil, nil, nil, ""
	c.mu.Unlock()

	if proc != nil && proc.Process != nil {
		_ = proc.Process.Kill()
		if exited != nil {
			select {
			case <-exited:
			case <-time.After(5 * time.Second):
			}
		}
	}
	if server != nil {
		_ = server.Close()
	}
	if listener != nil {
		_ = listener.Close()
	}
	if profile != "" {
		_ = os.RemoveAll(profile)
	}
}
