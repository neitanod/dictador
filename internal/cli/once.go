package cli

import (
	"bufio"
	"fmt"
	"os"
	"time"

	"github.com/neitanod/dictador/internal/audio"
	"github.com/neitanod/dictador/internal/stt"
	"github.com/neitanod/dictador/internal/x11"
)

// onceResult es lo que devuelve un dictado suelto.
type onceResult struct {
	Text    string  `json:"text"`
	Seconds float64 `json:"seconds"`
}

func cmdOnce(opts options, args []string) int {
	fs := subflags("once", opts, opts.out.stderr)
	seconds := fs.Float64("seconds", 0, "grabar N segundos en vez de esperar Enter")
	fs.Float64Var(seconds, "s", 0, "grabar N segundos en vez de esperar Enter")
	clipboard := fs.Bool("clipboard", false, "además copiar al clipboard")
	fs.BoolVar(clipboard, "c", false, "además copiar al clipboard")
	paste := fs.Bool("paste", false, "copiar y pegar en la ventana activa")
	fs.BoolVar(paste, "p", false, "copiar y pegar en la ventana activa")
	model := fs.String("model", "", "modelo para esta corrida")
	language := fs.String("language", "", "idioma para esta corrida")
	device := fs.String("device", "", "fuente de audio")
	engineName := fs.String("engine", "", "motor para esta corrida")
	quiet := fs.Bool("quiet", opts.quiet, "sólo el texto")
	fs.BoolVar(quiet, "q", opts.quiet, "sólo el texto")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	o := opts.out
	o.quiet = o.quiet || *quiet

	cfg, err := opts.load()
	if err != nil {
		o.fail(err, "CONFIG")
		return 1
	}
	if *model != "" {
		cfg.STT.Model = *model
	}
	if *language != "" {
		cfg.STT.Language = *language
	}
	if *engineName != "" {
		cfg.STT.Engine = *engineName
	}
	if *device != "" {
		cfg.Audio.Device = *device
	}

	engine, err := stt.Build(stt.OptionsFrom(cfg))
	if err != nil {
		o.fail(err, "ENGINE")
		return 1
	}
	defer engine.Close()

	o.info("Preparando el motor…")
	if err := engine.Load(); err != nil {
		o.fail(err, "ENGINE")
		return 1
	}

	live, isLive := engine.(stt.LiveEngine)
	recorder := audio.New(cfg.Audio.SampleRate, cfg.Audio.Device)
	if err := recorder.Start(); err != nil {
		o.fail(err, "AUDIO")
		return 1
	}
	if isLive {
		if err := live.StartLive(); err != nil {
			o.fail(err, "ENGINE")
			return 1
		}
	}

	started := time.Now()
	if *seconds > 0 {
		o.info("Grabando %.1fs…", *seconds)
		time.Sleep(time.Duration(*seconds * float64(time.Second)))
	} else {
		o.info("Grabando — Enter para cortar…")
		bufio.NewReader(os.Stdin).ReadString('\n')
	}
	samples := recorder.Stop()
	elapsed := time.Since(started).Seconds()

	if len(samples) == 0 && !isLive {
		fmt.Fprintf(o.stderr, "No entró audio. %s\n", recorder.Error())
		return 1
	}

	text, err := engine.Transcribe(samples, false)
	if err != nil {
		o.fail(err, "TRANSCRIBE")
		return 1
	}

	if *clipboard || *paste {
		clip, err := x11.NewClipboard()
		if err != nil {
			o.fail(err, "CLIPBOARD")
			return 1
		}
		defer clip.Close()
		if err := clip.Set(text); err != nil {
			o.fail(err, "CLIPBOARD")
			return 1
		}
		if *paste {
			if code := pasteNow(o, text); code != 0 {
				return code
			}
		}
	}

	if o.json {
		_ = o.printJSON(onceResult{Text: text, Seconds: round2(elapsed)})
	} else {
		fmt.Fprintln(o.stdout, text)
	}
	if text == "" {
		return 2
	}
	return 0
}

// pasteNow pega en la ventana que estaba enfocada al terminar.
func pasteNow(o out, _ string) int {
	conn, err := x11.Open()
	if err != nil {
		o.fail(err, "X11")
		return 1
	}
	defer conn.Close()
	if err := conn.EnableXTest(); err != nil {
		o.fail(err, "X11")
		return 1
	}
	target := conn.ActiveWindow()
	combo := "ctrl+v"
	if x11.IsTerminal(target.Class) {
		combo = "ctrl+shift+v"
	}
	// Un respiro para que el clipboard quede tomado antes del Ctrl+V.
	time.Sleep(60 * time.Millisecond)
	if err := conn.SendCombo(combo); err != nil {
		o.fail(err, "PASTE")
		return 1
	}
	return 0
}

func round2(v float64) float64 {
	return float64(int(v*100+0.5)) / 100
}
