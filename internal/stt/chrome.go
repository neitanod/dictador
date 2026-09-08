package stt

import (
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/neitanod/dictador/internal/config"
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
// Cómo se le pide al navegador que se cierre entero. Viene puesta desde el
// programa, que la sabe antes de servir esta página; el rebusque de pedirla
// después es para el caso en que todavía no la supiera.
const CIERRE_INICIAL = %s;
// Cuántos reintentos seguidos contra un server que no contesta antes de dar por
// muerto al dictador y cerrar este Chrome.
const HUERFANO_TRAS = %d;
const post = (kind, text, extra) =>
  fetch('/event', {method: 'POST', body: JSON.stringify(
    Object.assign({kind, text: text || ''}, extra || {}))})
    .catch(() => {});

// La traducción sale de acá adentro y no del programa en Go a propósito: este
// es el endpoint que usa el traductor del propio Chrome, contesta con CORS
// abierto y sin API key, y pedido desde una página es un pedido más de los que
// hace un navegador. El texto va por POST porque un dictado largo no entra en
// una URL.
const TRANSLATE = 'https://translate.googleapis.com/translate_a/single';

async function translate(job) {
  const url = TRANSLATE + '?client=gtx&sl=' + encodeURIComponent(job.from || 'auto') +
    '&tl=' + encodeURIComponent(job.to) + '&dt=t';
  try {
    const answer = await fetch(url, {
      method: 'POST',
      headers: {'Content-Type': 'application/x-www-form-urlencoded;charset=utf-8'},
      body: 'q=' + encodeURIComponent(job.text),
    });
    if (!answer.ok) throw new Error('HTTP ' + answer.status);
    const data = JSON.parse(await answer.text());
    // data[0] son los tramos en que Google partió el texto, cada uno con la
    // traducción primero y el original después; data[2] es el idioma que
    // detectó.
    const text = (data[0] || []).map(part => part[0] || '').join('');
    post('translation', text, {id: job.id, from: data[2] || ''});
  } catch (e) {
    post('translation-error', String(e && e.message || e), {id: job.id});
  }
}

let rec = null, heard = '';

// Con el puerto de manejo abierto —el que hace falta para traducir como la
// web— Chrome ya no se cierra cuando se cierra su última pestaña, así que
// window.close() no alcanza para no dejar un navegador huérfano dando vueltas.
// Por eso se pide de antemano, mientras el dictador vive, la dirección con la
// que se le pide al navegador que se cierre entero.
let cierreDelNavegador = CIERRE_INICIAL || null;
// Se pide con paciencia: al arrancar la página, el navegador todavía puede no
// estar listo para decir cómo se lo cierra, y el que la primera vez conteste
// vacío no puede dejarnos sin la dirección para siempre.
(async () => {
  for (let intento = 0; intento < 15 && !cierreDelNavegador; intento++) {
    try {
      const texto = (await (await fetch('/browser-ws')).text()).trim();
      if (texto) { cierreDelNavegador = texto; return; }
    } catch (e) { /* el dictador se fue; ya no hay a quién preguntarle */ }
    await new Promise(r => setTimeout(r, 1000));
  }
})();

function cerrarTodo() {
  if (!cierreDelNavegador) { window.close(); return; }
  try {
    const ws = new WebSocket(cierreDelNavegador);
    ws.onopen = () => ws.send(JSON.stringify({id: 1, method: 'Browser.close'}));
    // Si el navegador no se cierra en un segundo, al menos esta pestaña se va.
    setTimeout(() => window.close(), 1000);
  } catch (e) {
    window.close();
  }
}

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

// El long polling es también el pulso del dictador: mientras contesta —aunque
// sea un noop— está vivo. Si se murió de mala manera (un kill -9, un cuelgue,
// una sesión que se cierra) este Chrome se quedaba dando vueltas para siempre,
// reintentando contra un puerto que ya no existe: invisible, porque es headless,
// pero gastando CPU y un perfil en /tmp. Después de un rato sin nadie del otro
// lado se cierra solo.
async function loop() {
  let fallos = 0;
  for (;;) {
    try {
      const answer = await fetch('/command');
      const command = (await answer.text()).trim();
      fallos = 0;
      if (command === 'start') start();
      else if (command === 'stop') stop();
      // Sin await: traducir tarda lo que tarde internet, y mientras tanto la
      // tecla del dictado tiene que seguir contestando.
      else if (command.startsWith('translate ')) translate(JSON.parse(command.slice(10)));
    } catch (e) {
      if (++fallos >= HUERFANO_TRAS) { cerrarTodo(); return; }
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
	// translateTimeout es lo que se espera al traductor antes de decir que no
	// contestó y dejar el dictado como se dijo.
	translateTimeout time.Duration
	// translateMode es de dónde sale la traducción: "web" la pide en la página
	// de translate.google.com y "api" en el endpoint público.
	translateMode string
	// translateLangs son los idiomas configurados, para tener sus pestañas
	// listas antes del primer dictado.
	translateLangs []string
	// orphanRetries lo baja el test para no esperar los 20s de la vida real.
	orphanRetries int
	// verbose es el -v del daemon, para poder contar por qué la traducción
	// buena no salió.
	verbose bool

	commands chan string

	mu       sync.Mutex
	proc     *exec.Cmd
	server   *http.Server
	listener net.Listener
	profile  string
	// debugPort es por donde se le habla a este Chrome para manejar la página
	// del traductor. Lo elige Chrome y lo deja escrito en el perfil.
	debugPort int
	web       *webTranslator
	text      string
	final     string
	failure   string
	listening bool
	exited    chan struct{}

	// jobs son las traducciones pedidas y todavía sin contestar, por id.
	jobs   map[string]chan translationResult
	nextID int

	ready chan struct{}
	done  chan struct{}
	// readyOnce y doneOnce evitan cerrar dos veces el mismo canal cuando la
	// página manda un evento repetido.
	readyOnce sync.Once
	// warmOnce: las pestañas del traductor se preparan una vez por proceso.
	warmOnce sync.Once
}

// NewChrome arma el motor. No lanza nada hasta Load.
func NewChrome(opts Options) *Chrome {
	language := strings.TrimSpace(opts.STT.ChromeLanguage)
	if language == "" {
		language = GoogleLocale(opts.STT)
	}
	return &Chrome{
		binary:           ChromeBinary(opts.STT.ChromeBinary),
		language:         language,
		source:           opts.Device,
		readyTimeout:     seconds(opts.STT.ChromeReadyTimeoutS, 25),
		finalTimeout:     seconds(opts.STT.ChromeFinalTimeoutS, 6),
		translateTimeout: seconds(opts.Translate.TimeoutS, 10),
		translateMode:    strings.ToLower(strings.TrimSpace(opts.Translate.Mode)),
		verbose:          opts.Verbose,
		translateLangs:   translateLanguages(opts.Translate),
		headless:         opts.STT.ChromeHeadless,
		commands:         make(chan string, 4),
		jobs:             map[string]chan translationResult{},
		ready:            make(chan struct{}),
		done:             make(chan struct{}),
	}
}

// translateLanguages son los idiomas destino configurados, sin repetidos y en
// un orden fijo.
func translateLanguages(cfg config.Translate) []string {
	if !cfg.Enabled {
		return nil
	}
	seen := map[string]bool{}
	var out []string
	for _, language := range cfg.Keys {
		language = strings.TrimSpace(language)
		if language == "" || seen[language] {
			continue
		}
		seen[language] = true
		out = append(out, language)
	}
	sort.Strings(out)
	return out
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

// orphanRetries es la paciencia por default: 40 reintentos de 500ms son 20
// segundos sin nadie contestando, bastante más que cualquier hipo del server.
const orphanRetries = 40

// page es el HTML que Chrome va a correr, con el idioma ya adentro.
func (c *Chrome) page() string {
	lang, _ := json.Marshal(c.language)
	cierre, _ := json.Marshal(c.browserWS())
	retries := c.orphanRetries
	if retries <= 0 {
		retries = orphanRetries
	}
	return fmt.Sprintf(chromePage, lang, cierre, retries)
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
	c.warmOnce.Do(c.warmTranslator)
	return nil
}

// warmTranslator deja las pestañas del traductor listas antes de que hagan
// falta, sin hacer esperar a nadie.
func (c *Chrome) warmTranslator() {
	if c.translateMode == "api" || len(c.translateLangs) == 0 {
		return
	}
	go func() {
		web, err := c.translator()
		if err != nil {
			c.log("no pude preparar el traductor: %v", err)
			return
		}
		web.Warm(c.translateLangs)
		c.log("traductor listo para %s", strings.Join(c.translateLangs, ", "))
	}()
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
	// El pulso, para las pestañas del traductor. Va con CORS abierto porque lo
	// pregunta una página de Google, que es de otro origen: es un 200 vacío y
	// no dice nada de nadie.
	mux.HandleFunc("/alive", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.WriteHeader(http.StatusOK)
	})
	// Con qué dirección se le pide al navegador que se cierre entero. La página
	// la pide una vez, al arrancar, y se la guarda para cuando el dictador ya no
	// esté para contestar.
	mux.HandleFunc("/browser-ws", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(c.browserWS()))
	})
	mux.HandleFunc("/event", func(w http.ResponseWriter, r *http.Request) {
		var payload struct {
			Kind string `json:"kind"`
			Text string `json:"text"`
			ID   string `json:"id"`
			From string `json:"from"`
		}
		_ = json.NewDecoder(r.Body).Decode(&payload)
		if strings.HasPrefix(payload.Kind, "translation") {
			c.onTranslation(payload.Kind, payload.ID, payload.Text, payload.From)
			w.WriteHeader(http.StatusOK)
			return
		}
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
	// El dueño se anota antes de arrancar Chrome, no después: entre el mkdir y
	// el arranque puede pasar el barrido de otro dictador, y un perfil sin dueño
	// escrito parece de una versión vieja. Si no se pudo escribir seguimos igual
	// — el dictado importa más que la prolijidad del /tmp — y el barrido tiene
	// con qué salvarlo: mientras este proceso viva, su Chrome cuelga de él.
	_ = WriteOwner(profile)

	args := []string{
		"--disable-gpu",
		// Google mira el User-Agent para decidir qué traductor te da: al que
		// dice HeadlessChrome le sirve el modelo viejo, el que traduce palabra
		// por palabra. Con el User-Agent de un Chrome normal —mismo binario,
		// sigue siendo headless— aparece el que traduce por sentido.
		"--user-agent=" + BrowserUserAgent(c.binary),
		// Y el idioma del navegador es el tuyo, no el que Chrome trae de
		// fábrica: con la interfaz en inglés, Google sirve el traductor viejo.
		"--lang=" + c.language,
		"--accept-lang=" + c.language,
		"--no-first-run",
		"--no-default-browser-check",
		"--disable-extensions",
		"--user-data-dir=" + profile,
		// El permiso de micrófono se autoacepta: la página es nuestra y no hay
		// nadie para clickear el diálogo.
		"--use-fake-ui-for-media-stream",
	}
	// El puerto por el que se maneja Chrome se abre sólo si la traducción va a
	// salir de la página de Google: es lo único que lo necesita, y mientras
	// está abierto cualquier programa de esta máquina puede manejar ese Chrome.
	// Sin traducción por página, ni se abre.
	port := 0
	if c.translateMode != "api" {
		free, err := freePort()
		if err != nil {
			return errf("no pude reservar el puerto para manejar Chrome: %v", err)
		}
		port = free
		args = append(args,
			// El número lo ponemos nosotros. Pedirle a Chrome que lo elija él
			// —el clásico puerto 0— le dice al navegador que lo están
			// automatizando: prende navigator.webdriver, Google lo ve y te
			// sirve el traductor viejo, el que traduce palabra por palabra.
			"--remote-debugging-port="+strconv.Itoa(port),
			// Y que acepte que le hable la página del dictado, que es la que le
			// va a pedir que se cierre si el dictador se muere. Sólo esa
			// dirección: cualquier otra página que abra sigue sin poder
			// manejarlo.
			"--remote-allow-origins="+fmt.Sprintf("http://127.0.0.1:%d",
				c.listener.Addr().(*net.TCPAddr).Port),
			// La página del traductor vive en una pestaña de atrás, y a las
			// pestañas de atrás Chrome les frena los temporizadores para
			// ahorrar batería: la traducción tardaba y no llegaba nunca.
			"--disable-background-timer-throttling",
			"--disable-backgrounding-occluded-windows",
			"--disable-renderer-backgrounding",
		)
	}
	if c.headless {
		args = append([]string{"--headless=new"}, args...)
	}
	// La página del dictado va última, que es donde Chrome espera la dirección
	// que tiene que abrir.
	args = append(args, fmt.Sprintf("http://127.0.0.1:%d/", c.listener.Addr().(*net.TCPAddr).Port))
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
	c.debugPort = port
	c.web = nil
	return nil
}

// freePort pide un puerto libre de loopback y lo suelta enseguida.
//
// Entre soltarlo y que Chrome lo tome hay una rendija por la que otro programa
// podría meterse. Es la misma rendija que tiene cualquier programa que reserva
// un puerto así, y el precio de cerrarla —dejarle elegir a Chrome— es la
// traducción mala: entre las dos, esta.
func freePort() (int, error) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, err
	}
	defer listener.Close()
	return listener.Addr().(*net.TCPAddr).Port, nil
}

// devToolsPort lee el puerto que Chrome eligió para DevTools.
//
// Lo escribe en la primera línea de DevToolsActivePort apenas está listo, y
// tarda un momento en aparecer: es lo primero que hace y no lo último, así que
// esperar de a poco alcanza.
func devToolsPort(profile string, wait time.Duration) (int, error) {
	deadline := time.Now().Add(wait)
	path := filepath.Join(profile, "DevToolsActivePort")
	for {
		raw, err := os.ReadFile(path)
		if err == nil {
			line, _, _ := strings.Cut(string(raw), "\n")
			if port, err := strconv.Atoi(strings.TrimSpace(line)); err == nil && port > 0 {
				return port, nil
			}
		}
		if time.Now().After(deadline) {
			return 0, errf("Chrome no dijo por qué puerto se lo maneja")
		}
		time.Sleep(100 * time.Millisecond)
	}
}

// chromeVersion pregunta la versión al binario: "Google Chrome 150.0.7871.186".
func chromeVersion(binary string) string {
	if binary == "" {
		return ""
	}
	out, err := exec.Command(binary, "--version").Output()
	if err != nil {
		return ""
	}
	fields := strings.Fields(string(out))
	if len(fields) == 0 {
		return ""
	}
	return fields[len(fields)-1]
}

// fallbackVersion es la que se usa si el binario no contesta su versión. Que
// esté un poco atrasada no cambia nada: lo que importa es que no diga
// "HeadlessChrome".
const fallbackVersion = "140.0.0.0"

// BrowserUserAgent arma el User-Agent de un Chrome de escritorio con la versión
// del Chrome que tenemos.
func BrowserUserAgent(binary string) string {
	version := chromeVersion(binary)
	if version == "" {
		version = fallbackVersion
	}
	return "Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 (KHTML, like Gecko) " +
		"Chrome/" + version + " Safari/537.36"
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

// browserWS es la dirección con la que se le pide al navegador que se cierre.
//
// La da el propio Chrome en /json/version, y sólo sirve desde una página que
// Chrome tenga permitida: por eso al lanzarlo se le dice que acepte la nuestra.
func (c *Chrome) browserWS() string {
	c.mu.Lock()
	port := c.debugPort
	c.mu.Unlock()
	if port == 0 {
		return ""
	}
	client := &http.Client{Timeout: 3 * time.Second}
	res, err := client.Get(fmt.Sprintf("http://127.0.0.1:%d/json/version", port))
	if err != nil {
		return ""
	}
	defer res.Body.Close()
	var version struct {
		WebSocket string `json:"webSocketDebuggerUrl"`
	}
	if err := json.NewDecoder(res.Body).Decode(&version); err != nil {
		return ""
	}
	return version.WebSocket
}

// aliveURL es el pulso que las pestañas del traductor miran para saber si el
// dictador sigue vivo.
func (c *Chrome) aliveURL() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.listener == nil {
		return ""
	}
	return fmt.Sprintf("http://127.0.0.1:%d/alive", c.listener.Addr().(*net.TCPAddr).Port)
}

// translationResult es lo que vuelve de la página por una traducción pedida.
type translationResult struct {
	text string
	from string
	err  string
}

// Translate traduce el dictado usando el traductor de Google, desde el mismo
// Chrome que lo escuchó.
//
// El idioma de origen se deja en automático: la página que lo pide detecta sola
// de qué idioma viene, y así dictar en inglés y pedir portugués también anda,
// sin depender de en qué idioma esté configurado el reconocimiento.
func (c *Chrome) Translate(text, target string) (Translation, error) {
	text = strings.TrimSpace(text)
	target = strings.TrimSpace(target)
	if text == "" || target == "" {
		return Translation{Text: text}, nil
	}
	// Que Chrome esté vivo: el dictado pudo venir de un motor recién cambiado, o
	// del Chrome que se cerró solo por quedarse huérfano.
	if err := c.Load(); err != nil {
		return Translation{}, err
	}

	// La página de Google traduce mejor que el endpoint, y cuando no está
	// disponible el endpoint sigue estando: un dictado nunca se queda sin
	// traducir por culpa de una pestaña que no abrió.
	if c.translateMode != "api" {
		text, err := c.translateOnWeb(text, target)
		if err == nil {
			return Translation{Text: text, Web: true}, nil
		}
		c.log("la página del traductor no anduvo (%v); voy por el endpoint", err)
	}

	id := c.newJobID()
	job, _ := json.Marshal(struct {
		ID   string `json:"id"`
		To   string `json:"to"`
		Text string `json:"text"`
	}{ID: id, To: target, Text: text})

	answers := make(chan translationResult, 1)
	c.mu.Lock()
	c.jobs[id] = answers
	exited := c.exited
	c.mu.Unlock()
	defer func() {
		c.mu.Lock()
		delete(c.jobs, id)
		c.mu.Unlock()
	}()

	c.send("translate " + string(job))

	select {
	case answer := <-answers:
		if answer.err != "" {
			return Translation{}, errf("el traductor de Google no contestó: %s", answer.err)
		}
		if strings.TrimSpace(answer.text) == "" {
			return Translation{}, errf("el traductor de Google devolvió un texto vacío")
		}
		return Translation{Text: answer.text, From: answer.from}, nil
	case <-exited:
		return Translation{}, errf("Chrome se cerró antes de traducir")
	case <-time.After(c.translateTimeout):
		return Translation{}, errf("el traductor tardó más de %.0fs", c.translateTimeout.Seconds())
	}
}

// translateOnWeb traduce usando la página de translate.google.com, que es la
// que da la traducción por sentido.
func (c *Chrome) translateOnWeb(text, target string) (string, error) {
	web, err := c.translator()
	if err != nil {
		return "", err
	}
	return web.Translate(text, target)
}

// translator devuelve el manejador de la página, armándolo la primera vez.
func (c *Chrome) translator() (*webTranslator, error) {
	alive := c.aliveURL()
	c.mu.Lock()
	web, port, profile := c.web, c.debugPort, c.profile
	timeout := c.translateTimeout
	c.mu.Unlock()
	if web != nil {
		return web, nil
	}
	if profile == "" {
		return nil, errf("Chrome todavía no arrancó")
	}
	if port == 0 {
		port, err := devToolsPort(profile, 10*time.Second)
		if err != nil {
			return nil, err
		}
		c.mu.Lock()
		c.debugPort = port
		c.mu.Unlock()
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.web == nil {
		c.web = newWebTranslator(c.debugPort, timeout, c.language, alive)
	}
	return c.web, nil
}

// log deja dicho lo que pasó cuando alguien está mirando.
//
// El motor no tiene el log del daemon a mano —se arma antes— así que esto es
// para lo que no puede perderse en silencio: que la traducción buena falló y
// salió la otra.
func (c *Chrome) log(format string, args ...any) {
	if c.verbose {
		fmt.Printf("[dictador] "+format+"\n", args...)
	}
}

func (c *Chrome) newJobID() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.nextID++
	return fmt.Sprintf("t%d", c.nextID)
}

// onTranslation le entrega la respuesta al Translate que la está esperando.
//
// Una respuesta sin nadie esperándola se tira: es la que llegó después del
// timeout, y el dictado ya se pegó sin ella.
func (c *Chrome) onTranslation(kind, id, text, from string) {
	c.mu.Lock()
	answers, ok := c.jobs[id]
	c.mu.Unlock()
	if !ok {
		return
	}
	result := translationResult{text: text, from: from}
	if kind == "translation-error" {
		result.err = text
		if result.err == "" {
			result.err = "error desconocido"
		}
	}
	select {
	case answers <- result:
	default:
	}
}

// Close cierra Chrome, el server y el perfil temporal.
func (c *Chrome) Close() {
	c.mu.Lock()
	proc, server, listener, profile, exited := c.proc, c.server, c.listener, c.profile, c.exited
	web := c.web
	c.proc, c.server, c.listener, c.profile, c.web = nil, nil, nil, "", nil
	c.debugPort = 0
	c.mu.Unlock()

	// Las pestañas del traductor se cierran antes que el navegador: si ya se
	// murió no cuesta nada, y si sigue vivo —porque esto es un cambio de motor
	// y no una salida— no quedan colgadas.
	if web != nil {
		web.Close()
	}

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
