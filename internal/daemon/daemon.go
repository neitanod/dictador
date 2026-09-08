// Package daemon junta tecla, micrófono, motor de voz, ventanita y destino del
// texto.
//
// Máquina de estados
// ------------------
//
//	idle ──press──▶ armed ──(pasó el umbral)──▶ recording ──release──▶ thinking ──▶ idle
//	                  │                            │                       │
//	                  └── otra tecla / release  ────┴── audio muy corto     └── si hubo
//	                      antes del umbral              → cancelado             traducción
//	                                                                            ▼
//	                                                       idle ◀──(pega o Esc)── previewing
//
// `armed` existe para que la tecla siga sirviendo como modificador: recién a los
// ~180 ms de mantenerla asumimos que querés dictar. El micrófono igual arranca
// en el press, así no perdemos las primeras sílabas.
//
// Todo el estado vive en una sola goroutine y se mueve por canales. La versión
// Python necesitaba QObject, señales, tres QTimer y threads sueltos para lo
// mismo; acá es un `select`.
package daemon

import (
	"fmt"
	"strings"
	"time"

	"github.com/neitanod/dictador/internal/audio"
	"github.com/neitanod/dictador/internal/commands"
	"github.com/neitanod/dictador/internal/config"
	"github.com/neitanod/dictador/internal/history"
	"github.com/neitanod/dictador/internal/overlay"
	"github.com/neitanod/dictador/internal/stt"
	"github.com/neitanod/dictador/internal/translate"
	"github.com/neitanod/dictador/internal/webconfig"
	"github.com/neitanod/dictador/internal/x11"
)

// actionLabels es cómo se le cuenta al usuario qué se hizo con el texto.
var actionLabels = map[string]string{
	"paste":     "Pegado",
	"type":      "Tecleado",
	"clipboard": "Copiado al clipboard",
	"keep_open": "Listo",
}

type state int

const (
	idle state = iota
	armed
	recording
	thinking
	previewing
)

func (s state) String() string {
	switch s {
	case armed:
		return "armed"
	case recording:
		return "recording"
	case thinking:
		return "thinking"
	case previewing:
		return "previewing"
	default:
		return "idle"
	}
}

// result es lo que devuelve un worker de transcripción.
type result struct {
	text    string
	err     error
	gen     int
	partial bool
	// stage es un aviso de que el worker sigue trabajando y pasó a otra cosa
	// ("translating"), para que la ventanita lo diga sin esperar al final.
	stage string
	// language es el idioma al que se tradujo, vacío si el texto va como se
	// dijo. from es de dónde venía, según lo que detectó el traductor.
	language string
	from     string
	// warn es un problema que no impide entregar el dictado: la traducción que
	// no salió, con el texto original listo para pegar igual.
	warn string
}

// Daemon es la app corriendo.
type Daemon struct {
	cfg     config.Config
	verbose bool
	log     func(string)

	recorder *audio.Recorder
	engine   stt.Engine
	engErr   string
	listener *x11.Listener
	conn     *x11.Conn
	clip     clipboard
	ui       overlay.UI
	web      *webconfig.Server

	state      state
	generation int
	modelReady bool
	partialRun bool
	target     x11.Target

	// El texto en vivo se pide con este ticker, que sólo corre mientras grabás.
	partialTicker *time.Ticker
	partialEvery  time.Duration

	// El config.toml se puede editar a mano mientras esto anda, así que se lo
	// mira. reloadPending es un cambio visto en medio de un dictado, que espera
	// a que termines para aplicarse.
	watcher       *config.Watcher
	reloadPending bool

	// La pausa antes de pegar un dictado traducido: el plan espera acá mientras
	// lo leés, y Escape lo tira.
	previewTimer *time.Timer
	pending      *preview

	results chan result
	quit    chan struct{}

	// put ejecuta el plan en la ventana de destino. Es un campo y no un método
	// a secas para que las pruebas puedan mirar lo que se iba a pegar sin un
	// servidor X: lo que se prueba es la decisión, y el viaje a X ya está
	// probado del otro lado.
	put func(plan commands.Plan, action string) error
}

// clipboard es lo único que el daemon le pide al clipboard de X.
//
// Es una interfaz y no el tipo concreto para poder probar la pausa de la
// traducción —donde el texto cancelado termina en el clipboard— sin un
// servidor X de por medio.
type clipboard interface {
	Set(text string) error
	Close()
}

// preview es un dictado traducido esperando el visto bueno.
type preview struct {
	plan     commands.Plan
	action   string
	language string
}

// New arma el daemon: abre X, valida la tecla y prepara el motor.
func New(cfg config.Config, verbose bool) (*Daemon, error) {
	d := &Daemon{
		cfg:     cfg,
		verbose: verbose,
		results: make(chan result, 8),
		quit:    make(chan struct{}),
		ui:      overlay.Nop{},
	}
	d.put = d.paste
	d.log = func(msg string) {
		if d.verbose {
			fmt.Printf("[dictador] %s\n", msg)
		}
	}

	conn, err := x11.Open()
	if err != nil {
		return nil, err
	}
	if err := conn.EnableXTest(); err != nil {
		conn.Close()
		return nil, err
	}
	d.conn = conn

	clip, err := x11.NewClipboard()
	if err != nil {
		conn.Close()
		return nil, err
	}
	d.clip = clip

	listener, err := x11.NewListener(cfg.Hotkey.Key)
	if err != nil {
		clip.Close()
		conn.Close()
		return nil, err
	}
	d.listener = listener

	d.recorder = audio.New(cfg.Audio.SampleRate, cfg.Audio.Device)
	d.ui = pickUI(cfg, d.log)

	// La configuración se sirve desde el propio daemon: es lo que abre el click
	// en el overlay, y reemplaza al diálogo de Qt de la versión Python.
	web, err := webconfig.New(cfg)
	if err != nil {
		d.log("sin configuración web: " + err.Error())
	} else {
		d.web = web
	}

	// El archivo se mira aunque todavía no exista: que aparezca uno es otro de
	// los cambios que hay que leer.
	d.watcher = config.NewWatcher(configPath(cfg), time.Second)

	// Antes de armar el motor: si el dictador anterior murió de mala manera, su
	// Chrome puede seguir vivo gastando CPU contra un puerto que ya no existe.
	stt.SweepOrphanChromes(d.log)

	d.buildEngine()
	d.watchLanguageKeys()
	return d, nil
}

// watchLanguageKeys le pasa al listener las letras que eligen idioma, o
// ninguna si en esta configuración la traducción no corre.
//
// Con el motor equivocado se apagan a propósito: dejarlas escuchando haría que
// la "e" se sintiera distinta mientras dictás, para después pegar el texto en
// castellano igual.
func (d *Daemon) watchLanguageKeys() {
	if d.listener == nil {
		return
	}
	if !d.translationEnabled() {
		d.listener.WatchLanguages(nil)
		d.listener.SetSticky(false)
		return
	}
	d.listener.SetSticky(d.cfg.Translate.Sticky)
	for _, err := range d.listener.WatchLanguages(d.cfg.Translate.Keys) {
		d.log(err.Error())
	}
}

// ungrabLanguageKeys suelta las letras de idioma, si es que se agarraron.
func (d *Daemon) ungrabLanguageKeys() {
	if d.listener != nil {
		d.listener.UngrabLanguageKeys()
	}
}

// translationEnabled dice si este dictado se puede traducir: hace falta que
// esté prendida, que haya letras configuradas y que el motor sepa hacerlo.
func (d *Daemon) translationEnabled() bool {
	return d.cfg.Translate.Enabled &&
		len(d.cfg.Translate.Keys) > 0 &&
		d.engine != nil && stt.CanTranslate(d.engine)
}

// pickUI elige la ventanita: la dibujada si esta sesión la banca, y si no la
// notificación del escritorio, que es fea pero anda en cualquier lado.
func pickUI(cfg config.Config, log func(string)) overlay.UI {
	if !cfg.Overlay.Enabled {
		return overlay.Nop{}
	}
	window, err := overlay.NewWindow(cfg.Overlay)
	if err == nil {
		return window
	}
	log("sin overlay dibujado (" + err.Error() + "), va por notificaciones")
	if overlay.NotifyAvailable() {
		return overlay.NewNotify()
	}
	return overlay.Nop{}
}

// ConfigURL es dónde se sirve la configuración, o "" si no se pudo abrir.
func (d *Daemon) ConfigURL() string {
	if d.web == nil {
		return ""
	}
	return d.web.URL()
}

// Combo es la tecla que quedó escuchando, para poder anunciarla.
func (d *Daemon) Combo() *x11.Combo { return d.listener.Combo() }

// EngineLine es qué motor está en juego, en una línea.
func (d *Daemon) EngineLine() string {
	if d.engine == nil {
		return "sin motor de voz: " + d.engErr
	}
	return d.engine.Describe()
}

// EngineFailed dice si el motor no se pudo armar.
func (d *Daemon) EngineFailed() bool { return d.engine == nil }

// buildEngine arma el motor configurado. Si no se puede, deja el motivo a la
// vista en vez de morirse.
func (d *Daemon) buildEngine() bool {
	if d.engine != nil {
		d.engine.Close() // el anterior puede tener un Chrome colgando
	}
	opts := stt.OptionsFrom(d.cfg)
	opts.Verbose = d.verbose
	engine, err := stt.Build(opts)
	if err != nil {
		d.engine, d.engErr = nil, err.Error()
		return false
	}
	d.engine, d.engErr = engine, ""
	return true
}

// Run arranca el daemon y bloquea hasta que se lo pare.
func (d *Daemon) Run() error {
	go d.listener.Run()
	go d.preload()

	tick := time.NewTicker(80 * time.Millisecond)
	defer tick.Stop()

	d.partialEvery = partialEvery(d.cfg)
	d.partialTicker = time.NewTicker(time.Hour) // se reprograma al empezar a grabar
	d.partialTicker.Stop()
	defer d.partialTicker.Stop()

	var armTimer <-chan time.Time

	// El overlay dibujado avisa los clicks; la notificación no tiene dónde
	// hacer click, y ahí el canal se queda en nil y el select lo ignora.
	var clicks <-chan struct{}
	if clickable, ok := d.ui.(overlay.Clickable); ok {
		clicks = clickable.Clicked()
	}
	var saved <-chan webconfig.Values
	var killed <-chan struct{}
	if d.web != nil {
		saved = d.web.Saved()
		killed = d.web.Quit()
	}
	var edited <-chan struct{}
	if d.watcher != nil {
		edited = d.watcher.Changed()
	}

	for {
		select {
		case <-d.quit:
			return nil

		case event, ok := <-d.listener.Events():
			if !ok {
				return nil
			}
			switch event {
			case x11.Press:
				if d.onPress() {
					armTimer = time.After(d.holdThreshold())
				}
			case x11.Release:
				armTimer = nil
				d.onRelease()
			case x11.OtherKey:
				if d.state == armed && d.cfg.Hotkey.CancelOnOtherKey {
					armTimer = nil
					d.cancel("")
				}
			case x11.Cancel:
				armTimer = nil
				d.onEscape()
			}

		case hint := <-d.listener.Hints():
			// Apretaste (o soltaste) una letra de idioma mientras hablás: la
			// ventanita lo dice ahora, así no dictás a ciegas.
			d.showLanguageHint(hint)

		case <-armTimer:
			armTimer = nil
			d.startRecording()

		case <-tick.C:
			d.onTick()

		case <-d.partialTicker.C:
			d.requestPartial()

		case <-timerC(d.previewTimer):
			// Se acabó el tiempo de arrepentirse: va como está.
			d.commitPreview()

		case <-clicks:
			d.openSettings()

		case values := <-saved:
			d.applySettings(values)

		case <-edited:
			// Editaste el archivo a mano. Si estás dictando, el reload espera:
			// rearmar el motor en medio de una toma la perdería.
			if d.state == idle {
				d.reloadConfig()
			} else {
				d.reloadPending = true
			}

		case <-killed:
			// El botón "Matar al dictador" de la configuración. Salir del bucle
			// alcanza: quien llamó a Run cierra todo y el proceso termina.
			d.log("me mataron desde la configuración")
			return nil

		case res := <-d.results:
			d.onResult(res)
		}
	}
}

// timerC deja al select ignorar un timer que no existe: un canal nil no se
// elige nunca.
func timerC(t *time.Timer) <-chan time.Time {
	if t == nil {
		return nil
	}
	return t.C
}

// showLanguageHint escribe en la ventanita a qué idioma se va a traducir.
func (d *Daemon) showLanguageHint(hint x11.LanguageKey) {
	if d.state != recording {
		return
	}
	if hint.Language == "" {
		d.log("idioma: ninguno")
	} else {
		d.log("idioma: " + hint.Language + " (tecla " + hint.Key + ")")
	}
	if hint.Language == "" {
		d.ui.SetHint("Escuchando…")
		return
	}
	// Sin flecha: la fuente del overlay no tiene el glifo y se dibuja un
	// cuadrado, que es peor que no decir nada.
	d.ui.SetHint("Escuchando · sale en " + strings.ToLower(translate.Name(hint.Language)))
}

// onEscape es la tecla de arrepentirse.
func (d *Daemon) onEscape() {
	switch d.state {
	case previewing:
		d.dropPreview()
	case armed, recording:
		d.cancel("Cancelado")
	}
}

func (d *Daemon) holdThreshold() time.Duration {
	ms := d.cfg.Hotkey.HoldThresholdMs
	if ms <= 0 {
		ms = 180
	}
	return time.Duration(ms) * time.Millisecond
}

// preload deja el motor listo antes del primer dictado.
func (d *Daemon) preload() {
	started := time.Now()
	if d.engine == nil {
		d.results <- result{err: fmt.Errorf("%s", d.engErr), gen: -1}
		return
	}
	if err := d.engine.Load(); err != nil {
		d.results <- result{err: err, gen: -1}
		return
	}
	d.results <- result{text: fmt.Sprintf("%.1fs", time.Since(started).Seconds()), gen: -1}
}

// ---- configuración -------------------------------------------------------

// openSettings es el click en el overlay: corta el dictado y abre la config.
//
// Cortar primero no es cortesía: si siguiéramos grabando, al soltar la tecla el
// texto se pegaría en la ventana de configuración o en lo que hubiera detrás.
func (d *Daemon) openSettings() {
	if d.state != idle {
		d.cancel("")
	} else {
		d.ui.Dismiss()
	}
	if d.web == nil {
		d.showError("no pude abrir la configuración web")
		return
	}
	if err := d.web.Open(); err != nil {
		d.showError("no pude abrir el browser: " + err.Error())
		return
	}
	d.log("abrí la configuración en " + d.web.URL())
}

// applySettings cambia el motor en caliente, sin reiniciar el daemon.
func (d *Daemon) applySettings(values webconfig.Values) {
	d.cfg.STT.Engine = values.Engine
	d.cfg.STT.GoogleAPIKey = values.GoogleAPIKey
	d.cfg.STT.GoogleLanguage = values.GoogleLanguage
	d.cfg.STT.ChromeLanguage = values.ChromeLanguage
	d.cfg.Overlay.Screen = values.Screen
	d.cfg.Overlay.Position = values.Position
	d.cfg.Commands.Enabled = values.Commands
	d.cfg.Commands.Replacements = values.Replacements
	d.cfg.Action.TrailingSpace = values.TrailingSpace
	d.cfg.Translate.Enabled = values.Translate
	d.cfg.Translate.Mode = values.TranslateMode
	d.cfg.Translate.Sticky = values.TranslateSticky
	d.cfg.Translate.Keys = values.TranslateKeys
	// Dónde aparece la ventanita se cambia sin reiniciar nada: la próxima vez
	// que dictes ya aparece donde la mandaste.
	if placeable, ok := d.ui.(overlay.Placeable); ok {
		placeable.SetPlacement(values.Screen, values.Position)
	}
	d.modelReady = false
	if !d.buildEngine() {
		d.watchLanguageKeys()
		d.showError(d.engErr)
		return
	}
	// Las letras de idioma dependen del motor que quedó: con uno que no traduce
	// se apagan solas.
	d.watchLanguageKeys()
	if d.web != nil {
		d.web.Update(d.cfg)
	}
	d.log("motor nuevo: " + d.EngineLine())
	go d.preload()
}

// ---- eventos de tecla ----------------------------------------------------

// onPress devuelve true si hay que armar el temporizador del umbral.
func (d *Daemon) onPress() bool {
	if d.cfg.Hotkey.Mode == "toggle" && d.state == recording {
		d.finish()
		return false
	}
	// Volver a apretar la tecla con una traducción esperando el visto bueno es
	// darlo por bueno: se pega y arranca el dictado siguiente sin esperar.
	if d.state == previewing {
		d.commitPreview()
	}
	if d.state != idle {
		return false
	}
	// Sin conexión a X ni grabador no hay dictado que empezar. En la vida real
	// no pasa —New falla antes—; en las pruebas del pegado sí, y ahí lo que
	// importa ya ocurrió arriba.
	if d.conn == nil || d.recorder == nil {
		return false
	}
	// El destino se guarda ANTES de grabar: cuando el dictado termina el foco
	// puede haberse ido a cualquier parte.
	d.target = d.conn.ActiveWindow()
	if err := d.recorder.Start(); err != nil {
		d.showError(fmt.Sprintf("No pude abrir el micrófono: %v", err))
		return false
	}
	d.state = armed
	return true
}

func (d *Daemon) onRelease() {
	if d.state == armed {
		d.cancel("") // fue la tecla usada como tecla, no como dictado
		return
	}
	if d.cfg.Hotkey.Mode != "toggle" && d.state == recording {
		d.finish()
	}
}

// stopPartial apaga el texto en vivo: el dictado terminó o se canceló.
func (d *Daemon) stopPartial() {
	if d.partialTicker != nil {
		d.partialTicker.Stop()
	}
}

// ---- grabación ------------------------------------------------------------

func (d *Daemon) startRecording() {
	if d.state != armed {
		return
	}
	d.state = recording
	d.generation++

	hint := "Escuchando…"
	if !d.modelReady {
		hint = "Escuchando (cargando modelo)…"
	}
	d.ui.BeginListening(hint)

	// Las letras de idioma se agarran mientras dura la grabación: elegir el
	// idioma no tiene que escribir la letra en la app de atrás.
	if d.translationEnabled() && d.listener != nil {
		if err := d.listener.GrabLanguageKeys(); err != nil {
			d.log("no pude agarrar las teclas de idioma: " + err.Error())
		}
	}

	// Los motores vivos escuchan el micrófono ellos mismos: hay que avisarles
	// que arrancó el dictado, no pasarles el audio después.
	if live, ok := d.engine.(stt.LiveEngine); ok {
		if err := live.StartLive(); err != nil {
			d.log(fmt.Sprintf("el motor vivo no arrancó: %v", err))
		}
	}
	// Con un motor por red no hay texto en vivo: cada parcial sería un viaje a
	// internet y una llamada facturada.
	if d.partialEvery > 0 && d.engine != nil && d.engine.SupportsPartial() {
		d.partialTicker.Reset(d.partialEvery)
	}
	d.log(fmt.Sprintf("grabando (destino: %s %d)", orUnknown(d.target.Class), d.target.Window))
}

func orUnknown(class string) string {
	if class == "" {
		return "desconocido"
	}
	return class
}

func (d *Daemon) onTick() {
	// El config que cambió mientras dictabas se aplica ahora, con el dictado ya
	// entregado y la próxima toma todavía sin empezar.
	if d.reloadPending && d.state == idle {
		d.reloadPending = false
		d.reloadConfig()
	}
	if d.state != recording {
		return
	}
	d.ui.SetMeter(d.recorder.Level(), d.recorder.Seconds())
	max := d.cfg.Limits.MaxSeconds
	if max > 0 && d.recorder.Seconds() >= max {
		d.log("corté por max_seconds")
		d.finish()
	}
}

func (d *Daemon) requestPartial() {
	if d.state != recording || d.partialRun || !d.modelReady || d.engine == nil {
		return
	}
	// Con un motor vivo el texto ya está transcripto del otro lado: es leer una
	// variable, no transcribir.
	if live, ok := d.engine.(stt.LiveEngine); ok {
		d.applyPartial(live.PartialText(), d.generation)
		return
	}
	samples := d.recorder.Snapshot()
	if len(samples) < int(float64(d.recorder.SampleRate())*0.6) {
		return
	}
	d.partialRun = true
	gen := d.generation
	engine := d.engine
	go func() {
		text, err := engine.Transcribe(samples, true)
		d.results <- result{text: text, err: err, gen: gen, partial: true}
	}()
}

func (d *Daemon) applyPartial(text string, gen int) {
	if gen != d.generation || d.state != recording || text == "" {
		return
	}
	d.ui.SetPartial(Preview(text, d.cfg))
}

// Preview es cómo se ve el dictado mientras hablás.
//
// Los comandos van aplicados acá también: leer "abre pregunta cómo andás signo
// de pregunta" en la ventanita y que recién al soltar aparezca "¿cómo andás?"
// obliga a traducir de cabeza lo que va a pasar. Lo que se lee es lo que se va
// a escribir.
//
// Los retoques de entrega —sacar el punto final, el espacio de atrás— quedan
// afuera a propósito: el parcial todavía está creciendo, y el punto de ahora no
// es el final de nada.
func Preview(text string, cfg config.Config) string {
	return commands.Compile(text, commands.OptionsFrom(cfg)).Text()
}

// ---- cierre del dictado ---------------------------------------------------

func (d *Daemon) cancel(message string) {
	d.stopPartial()
	d.ungrabLanguageKeys()
	d.generation++
	if d.recorder.Running() {
		d.recorder.Stop()
	}
	// Cancelar tiene que cerrarle el micrófono al motor vivo: si no, Chrome
	// sigue escuchando un dictado que ya nadie quiere.
	if live, ok := d.engine.(stt.LiveEngine); ok {
		live.AbortLive()
	}
	d.state = idle
	if message != "" {
		d.ui.SetDone("", message, 1200*time.Millisecond)
	} else {
		d.ui.Dismiss()
	}
	if message != "" {
		d.log("cancelado: " + message)
	} else {
		d.log("cancelado")
	}
}

func (d *Daemon) finish() {
	d.stopPartial()
	d.ungrabLanguageKeys()
	// El idioma se decidió en el instante en que soltaste, y el listener lo
	// congeló ahí: preguntarlo ahora daría lo que esté apretado un rato
	// después, que ya es otra cosa.
	language := ""
	if d.translationEnabled() && d.listener != nil {
		language = d.listener.ReleaseLanguage().Language
	}
	samples := d.recorder.Stop()
	seconds := float64(len(samples)) / float64(d.recorder.SampleRate())
	micError := d.recorder.Error()

	min := d.cfg.Limits.MinSeconds
	if seconds < min {
		if micError != "" {
			d.cancel("Micrófono: " + micError)
		} else {
			d.cancel("Muy corto, no grabé nada")
		}
		return
	}

	d.state = thinking
	d.generation++
	gen := d.generation
	d.ui.SetThinking(fmt.Sprintf("Transcribiendo %.1fs…", seconds))
	if language != "" {
		d.log("va traducido a " + language)
	}

	engine := d.engine
	if engine == nil {
		d.results <- result{err: fmt.Errorf("%s", d.engErr), gen: gen}
		return
	}
	ready := d.modelReady
	cfg := d.cfg
	go func() {
		for _, res := range dictate(engine, samples, ready, language, cfg, d.log) {
			res.gen = gen
			d.results <- res
		}
	}()
}

// dictate es el trabajo del worker: transcribir, y si el dictado va traducido,
// traducir. Devuelve lo que hay que contarle al daemon, en orden.
//
// Está afuera del daemon para poder probar la regla que decide el resultado sin
// micrófono ni servidor X: los comandos hablados corren antes de traducir.
func dictate(engine stt.Engine, samples []float32, ready bool, language string,
	cfg config.Config, log func(string)) []result {
	started := time.Now()
	if !ready {
		if err := engine.Load(); err != nil {
			return []result{{err: err}}
		}
	}
	text, err := engine.Transcribe(samples, false)
	if err != nil {
		return []result{{err: err}}
	}
	log(fmt.Sprintf("transcripción final en %.2fs", time.Since(started).Seconds()))
	if language == "" {
		return []result{{text: text}}
	}
	// Los comandos hablados se aplican ANTES de traducir: se dicen en
	// castellano, y mandarle "coma" al traductor devuelve la palabra "comma" en
	// vez del signo.
	spoken := commands.Compile(text, commands.OptionsFrom(cfg)).Text()
	if strings.TrimSpace(spoken) == "" {
		return []result{{text: text}}
	}
	avisos := []result{{stage: "translating", language: language}}
	translated, err := translateText(engine, spoken, language)
	if err != nil {
		// Un traductor que no contesta no puede costarte el dictado: va el
		// original, con el aviso de por qué.
		log("la traducción falló: " + err.Error())
		return append(avisos, result{text: text, warn: "sin traducir: " + reason(err)})
	}
	res := result{text: translated.Text, language: language, from: translated.From}
	if !translated.Web {
		// La traducción buena sale de la página de Google; ésta salió del
		// endpoint, que traduce más literal. Decirlo evita el rato de creer que
		// el traductor empeoró sin motivo.
		res.warn = "traducción literal: la página de Google no contestó"
	}
	return append(avisos, res)
}

// translateText traduce con el motor, si es de los que saben.
func translateText(engine stt.Engine, text, language string) (stt.Translation, error) {
	translator, ok := engine.(stt.Translator)
	if !ok {
		return stt.Translation{}, fmt.Errorf("el motor %s no traduce", engine.Name())
	}
	return translator.Translate(text, language)
}

func (d *Daemon) onResult(res result) {
	// gen -1 es la precarga del motor, que no pertenece a ningún dictado.
	if res.gen == -1 {
		d.modelReady = res.err == nil
		if res.err != nil {
			d.log("el motor de voz falló: " + res.err.Error())
			d.ui.SetError("No pude arrancar el motor de voz: " + res.err.Error())
			return
		}
		d.log(fmt.Sprintf("listo para dictar — %s, listo en %s", d.EngineLine(), res.text))
		return
	}
	if res.stage == "translating" {
		if res.gen == d.generation && d.state == thinking {
			d.ui.SetThinking("Traduciendo al " + translate.Name(res.language) + "…")
		}
		return
	}
	if res.partial {
		d.partialRun = false
		if res.err != nil {
			d.log("parcial falló: " + res.err.Error())
			return
		}
		d.applyPartial(res.text, res.gen)
		return
	}
	if res.gen != d.generation {
		return // llegó tarde: ya hay otro dictado en curso
	}
	d.state = idle
	if res.err != nil {
		d.showError("Falló la transcripción: " + res.err.Error())
		return
	}
	d.modelReady = true
	d.deliver(res)
}

// deliver hace con el texto lo que diga [action].
func (d *Daemon) deliver(res result) {
	plan := Prepare(res.text, d.cfg)
	if res.language != "" {
		plan = PrepareTranslated(res.text, d.cfg)
	}
	if plan.Text() == "" {
		d.ui.SetDone("", "No se entendió nada", 1600*time.Millisecond)
		return
	}
	action := d.cfg.Action.OnRelease
	// La pausa para arrepentirse es sólo del dictado traducido, y sólo cuando
	// el texto va a salir disparado a otra ventana: lo que queda en el
	// clipboard no se pega en ningún lado y no hay nada que frenar.
	if res.language != "" && d.previewDelay() > 0 && action != "clipboard" && action != "keep_open" {
		d.startPreview(plan, action, res.language)
		return
	}
	d.apply(plan, action, res.language, res.warn)
}

// apply ejecuta el plan en el destino y cuenta cómo salió.
func (d *Daemon) apply(plan commands.Plan, action, language, warn string) {
	text := plan.Text()
	status := statusFor(action, language)
	if err := d.put(plan, action); err != nil {
		_ = d.clip.Set(text) // al menos que no se pierda
		status = fmt.Sprintf("Quedó en el clipboard (%v)", err)
	} else if warn != "" {
		status += " · " + warn
	}
	if err := history.Append(history.Entry{
		Text: text, Action: action, Target: d.target.Class,
	}); err != nil {
		d.log("no pude escribir el historial: " + err.Error())
	}
	if action == "keep_open" {
		d.ui.SetDone(text, "Copiado — se queda abierto", 0)
		return
	}
	d.ui.SetDone(text, status, time.Duration(d.cfg.Overlay.HideDelayMs)*time.Millisecond)
}

// statusFor es lo que dice la ventanita cuando el texto ya salió.
func statusFor(action, language string) string {
	status := actionLabels[action]
	if status == "" {
		status = action
	}
	if language != "" {
		status += " en " + strings.ToLower(translate.Name(language))
	}
	return status
}

// ---- la pausa antes de pegar una traducción ------------------------------

// previewDelay es cuánto se muestra la traducción antes de pegarla.
func (d *Daemon) previewDelay() time.Duration {
	ms := d.cfg.Translate.PreviewMs
	if ms < 0 {
		ms = 0
	}
	return time.Duration(ms) * time.Millisecond
}

// startPreview muestra el texto ya traducido y espera un momento antes de
// pegarlo.
//
// Es el segundo que pediste para leerlo: una traducción puede salir torcida, y
// darse cuenta después de que se pegó en el chat es tarde. Escape lo tira.
func (d *Daemon) startPreview(plan commands.Plan, action, language string) {
	d.pending = &preview{plan: plan, action: action, language: language}
	d.state = previewing
	// Escape es nuestro mientras dura la pausa, así cancelar el pegado no le
	// cierra además el diálogo a la ventana de adelante.
	if d.listener != nil {
		if err := d.listener.GrabCancelKey(); err != nil {
			d.log("no pude agarrar Escape: " + err.Error())
		}
	}
	delay := d.previewDelay()
	d.ui.SetDone(plan.Text(), fmt.Sprintf("%s · se pega en %s · Esc cancela",
		translate.Name(language), formatDelay(delay)), 0)
	if d.previewTimer != nil {
		d.previewTimer.Stop()
	}
	d.previewTimer = time.NewTimer(delay)
}

// commitPreview pega lo que estaba esperando.
func (d *Daemon) commitPreview() {
	pending := d.endPreview()
	if pending == nil {
		return
	}
	d.apply(pending.plan, pending.action, pending.language, "")
}

// dropPreview tira el pegado y deja el texto en el clipboard.
//
// Que quede en el clipboard es lo que hace barata la cancelación: si te
// arrepentiste del pegado pero la traducción te servía, la tenés a un Ctrl+V.
func (d *Daemon) dropPreview() {
	pending := d.endPreview()
	if pending == nil {
		return
	}
	text := pending.plan.Text()
	status := "Cancelado — te lo dejo en el clipboard"
	if err := d.clip.Set(text); err != nil {
		status = "Cancelado"
	}
	d.log("pegado cancelado con Escape")
	d.ui.SetDone(text, status, 2200*time.Millisecond)
}

// endPreview cierra la pausa y devuelve lo que estaba esperando.
func (d *Daemon) endPreview() *preview {
	if d.previewTimer != nil {
		d.previewTimer.Stop()
		d.previewTimer = nil
	}
	pending := d.pending
	d.pending = nil
	if d.state == previewing {
		d.state = idle
	}
	if d.listener != nil {
		d.listener.UngrabCancelKey()
	}
	return pending
}

// formatDelay escribe la espera como se lee: "1 s", "1,2 s".
func formatDelay(delay time.Duration) string {
	seconds := delay.Seconds()
	if seconds == float64(int(seconds)) {
		return fmt.Sprintf("%d s", int(seconds))
	}
	return strings.Replace(fmt.Sprintf("%.1f s", seconds), ".", ",", 1)
}

// reason es el mensaje de un error, para meterlo en una línea de estado.
func reason(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

// paste ejecuta el plan en el destino que corresponda.
//
// El clipboard no tiene cursor ni teclado: ahí el plan se aplana y va como
// texto. En la ventana, en cambio, se ejecuta paso por paso, que es lo que hace
// que "entre corchetes" deje el cursor adentro.
func (d *Daemon) paste(plan commands.Plan, action string) error {
	switch action {
	case "clipboard", "keep_open":
		return d.clip.Set(plan.Text())
	case "type":
		if d.cfg.Action.RestoreFocus {
			d.conn.Focus(d.target.Window)
			time.Sleep(60 * time.Millisecond)
		}
		for _, step := range plan.Steps {
			if step.Key != "" {
				if err := d.conn.SendCombo(step.Key); err != nil {
					return err
				}
				continue
			}
			if err := d.conn.TypeText(step.Text, 12*time.Millisecond); err != nil {
				return err
			}
		}
		return nil
	default:
		focused := false
		combo := "ctrl+v"
		if x11.IsTerminalWith(d.target.Class, d.cfg.Action.TerminalClasses) {
			combo = "ctrl+shift+v"
		}
		for i, step := range plan.Steps {
			if step.Text != "" {
				if err := d.clip.Set(step.Text); err != nil {
					return err
				}
			}
			if !focused {
				if d.cfg.Action.RestoreFocus && d.conn.Focus(d.target.Window) {
					// Que el WM termine de mover el foco antes del Ctrl+V.
					time.Sleep(60 * time.Millisecond)
				}
				focused = true
			}
			if i > 0 {
				// Un pegado y la tecla que le sigue no pueden salir juntos: la
				// app de enfrente todavía está metiendo el texto anterior.
				time.Sleep(45 * time.Millisecond)
			}
			key := step.Key
			if key == "" {
				key = combo
			}
			if err := d.conn.SendCombo(key); err != nil {
				return err
			}
		}
		return nil
	}
}

// Prepare arma el plan del dictado: comandos hablados primero, retoques finales
// después.
func Prepare(text string, cfg config.Config) commands.Plan {
	plan := commands.Compile(text, commands.OptionsFrom(cfg))
	if plan.Empty() {
		return plan
	}
	if cfg.Action.StripFinalPeriod {
		plan.StripFinalPeriod()
	}
	if cfg.Action.TrailingSpace {
		plan.AppendText(" ")
	}
	return plan
}

// PrepareTranslated arma el plan de un dictado que ya pasó por el traductor.
//
// Los comandos hablados no se vuelven a aplicar: se ejecutaron antes de
// traducir, y correrlos sobre el texto en inglés convertiría un "coma" que
// ahora dice "comma" en un signo que nadie pidió. Lo que queda es texto plano
// con los retoques de entrega.
func PrepareTranslated(text string, cfg config.Config) commands.Plan {
	text = strings.TrimSpace(text)
	if text == "" {
		return commands.Plan{}
	}
	plan := commands.Plan{Steps: []commands.Step{{Text: text}}}
	if cfg.Action.StripFinalPeriod {
		plan.StripFinalPeriod()
	}
	if cfg.Action.TrailingSpace {
		plan.AppendText(" ")
	}
	return plan
}

// Postprocess limpia el texto antes de entregarlo, sin ejecutar comandos.
//
// Es lo que usa `dictador once`, que escribe en stdout y no tiene dónde mandar
// una tecla.
func Postprocess(text string, action config.Action) string {
	plan := commands.Compile(text, commands.Options{})
	if plan.Empty() {
		return ""
	}
	if action.StripFinalPeriod {
		plan.StripFinalPeriod()
	}
	if action.TrailingSpace {
		plan.AppendText(" ")
	}
	return plan.Text()
}

func (d *Daemon) showError(message string) {
	d.log(message)
	d.ui.SetError(message)
}

// Stop corta el daemon y suelta todo.
func (d *Daemon) Stop() {
	select {
	case <-d.quit:
	default:
		close(d.quit)
	}
	// Los grabs se sueltan antes de cerrar la conexión: X los libera solo al
	// cerrarla, y soltarlos acá deja el teclado limpio también cuando el
	// listener sigue vivo un rato más.
	d.ungrabLanguageKeys()
	if d.listener != nil {
		d.listener.UngrabCancelKey()
	}
	d.listener.Stop()
	if d.watcher != nil {
		d.watcher.Close()
	}
	if d.recorder.Running() {
		d.recorder.Stop()
	}
	if d.engine != nil {
		d.engine.Close()
	}
	d.ui.Close()
	if d.web != nil {
		d.web.Close()
	}
	d.clip.Close()
	d.conn.Close()
}
