// Package daemon junta tecla, micrófono, motor de voz, ventanita y destino del
// texto.
//
// Máquina de estados
// ------------------
//
//	idle ──press──▶ armed ──(pasó el umbral)──▶ recording ──release──▶ thinking ──▶ idle
//	                  │                            │
//	                  └── otra tecla / release  ────┴── audio muy corto
//	                      antes del umbral              → cancelado
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
	"github.com/neitanod/dictador/internal/config"
	"github.com/neitanod/dictador/internal/history"
	"github.com/neitanod/dictador/internal/overlay"
	"github.com/neitanod/dictador/internal/stt"
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
)

func (s state) String() string {
	switch s {
	case armed:
		return "armed"
	case recording:
		return "recording"
	case thinking:
		return "thinking"
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
	clip     *x11.Clipboard
	ui       overlay.UI

	state      state
	generation int
	modelReady bool
	partialRun bool
	target     x11.Target

	// El texto en vivo se pide con este ticker, que sólo corre mientras grabás.
	partialTicker *time.Ticker
	partialEvery  time.Duration

	results chan result
	quit    chan struct{}
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
	if cfg.Overlay.Enabled && overlay.NotifyAvailable() {
		d.ui = overlay.NewNotify()
	}
	d.buildEngine()
	return d, nil
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
	engine, err := stt.Build(stt.OptionsFrom(d.cfg))
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

	d.partialEvery = time.Duration(d.cfg.STT.PartialIntervalMs) * time.Millisecond
	d.partialTicker = time.NewTicker(time.Hour) // se reprograma al empezar a grabar
	d.partialTicker.Stop()
	defer d.partialTicker.Stop()

	var armTimer <-chan time.Time

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
			}

		case <-armTimer:
			armTimer = nil
			d.startRecording()

		case <-tick.C:
			d.onTick()

		case <-d.partialTicker.C:
			d.requestPartial()

		case res := <-d.results:
			d.onResult(res)
		}
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

// ---- eventos de tecla ----------------------------------------------------

// onPress devuelve true si hay que armar el temporizador del umbral.
func (d *Daemon) onPress() bool {
	if d.cfg.Hotkey.Mode == "toggle" && d.state == recording {
		d.finish()
		return false
	}
	if d.state != idle {
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
	d.ui.SetPartial(text)
}

// ---- cierre del dictado ---------------------------------------------------

func (d *Daemon) cancel(message string) {
	d.stopPartial()
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

	engine := d.engine
	if engine == nil {
		d.results <- result{err: fmt.Errorf("%s", d.engErr), gen: gen}
		return
	}
	ready := d.modelReady
	go func() {
		started := time.Now()
		if !ready {
			if err := engine.Load(); err != nil {
				d.results <- result{err: err, gen: gen}
				return
			}
		}
		text, err := engine.Transcribe(samples, false)
		if err == nil {
			d.log(fmt.Sprintf("transcripción final en %.2fs", time.Since(started).Seconds()))
		}
		d.results <- result{text: text, err: err, gen: gen}
	}()
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
	d.deliver(res.text)
}

// deliver hace con el texto lo que diga [action].
func (d *Daemon) deliver(text string) {
	text = Postprocess(text, d.cfg.Action)
	if text == "" {
		d.ui.SetDone("", "No se entendió nada", 1600*time.Millisecond)
		return
	}
	action := d.cfg.Action.OnRelease
	status := actionLabels[action]
	if status == "" {
		status = action
	}
	if err := d.put(text, action); err != nil {
		_ = d.clip.Set(text) // al menos que no se pierda
		status = fmt.Sprintf("Quedó en el clipboard (%v)", err)
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

func (d *Daemon) put(text, action string) error {
	switch action {
	case "clipboard", "keep_open":
		return d.clip.Set(text)
	case "type":
		if d.cfg.Action.RestoreFocus {
			d.conn.Focus(d.target.Window)
			time.Sleep(60 * time.Millisecond)
		}
		return d.conn.TypeText(text, 12*time.Millisecond)
	default:
		if err := d.clip.Set(text); err != nil {
			return err
		}
		if d.cfg.Action.RestoreFocus && d.conn.Focus(d.target.Window) {
			// Que el WM termine de mover el foco antes del Ctrl+V.
			time.Sleep(60 * time.Millisecond)
		}
		combo := "ctrl+v"
		if x11.IsTerminal(d.target.Class) {
			combo = "ctrl+shift+v"
		}
		return d.conn.SendCombo(combo)
	}
}

// Postprocess limpia el texto antes de entregarlo.
func Postprocess(text string, action config.Action) string {
	text = strings.Join(strings.Fields(text), " ")
	if text == "" {
		return ""
	}
	if action.StripFinalPeriod && strings.HasSuffix(text, ".") {
		text = strings.TrimSuffix(text, ".")
	}
	if action.TrailingSpace {
		text += " "
	}
	return text
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
	d.listener.Stop()
	if d.recorder.Running() {
		d.recorder.Stop()
	}
	if d.engine != nil {
		d.engine.Close()
	}
	d.ui.Close()
	d.clip.Close()
	d.conn.Close()
}
