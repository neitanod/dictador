package cli

import (
	"encoding/binary"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/neitanod/dictador/internal/audio"
	"github.com/neitanod/dictador/internal/stt"
)

// BenchResult es lo que tardó y lo que entendió cada motor.
type BenchResult struct {
	Engine  string  `json:"engine"`
	Seconds float64 `json:"seconds"`
	Text    string  `json:"text"`
	Error   string  `json:"error,omitempty"`
}

// BenchReport es la comparación completa.
type BenchReport struct {
	AudioSeconds float64       `json:"audio_seconds"`
	Results      []BenchResult `json:"results"`
}

// cmdBench compara motores con TU voz y TU micrófono, que es lo que importa.
//
// El bench del original comparaba modelos de Whisper, porque el modelo estaba
// adentro del proceso y cambiarlo era una línea. Acá el modelo lo tiene el
// whisper-server, así que lo que se compara son los motores: es la elección que
// hoy tenés a mano.
func cmdBench(opts *options, args []string) int {
	fs := subflags("bench", opts, opts.out.stderr)
	seconds := fs.Float64("seconds", 6, "cuánto grabar")
	fs.Float64Var(seconds, "s", 6, "cuánto grabar")
	engines := fs.String("engines", "", "lista separada por comas (default: los disponibles)")
	fs.StringVar(engines, "e", "", "lista separada por comas")
	file := fs.String("file", "", "usar un wav mono 16 kHz en vez de grabar")
	fs.StringVar(file, "f", "", "usar un wav mono 16 kHz en vez de grabar")
	device := fs.String("device", "", "fuente de audio")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	opts.refresh()
	o := opts.out

	cfg, err := opts.load()
	if err != nil {
		o.fail(err, "CONFIG")
		return 1
	}
	if *device != "" {
		cfg.Audio.Device = *device
	}

	names := splitList(*engines)
	if len(names) == 0 {
		names = stt.Available(stt.OptionsFrom(cfg))
	}

	// Los motores vivos escuchan ellos mismos: no se los puede comparar contra
	// una grabación, y decirlo es mejor que dar un cero que parece un resultado.
	var samples []float32
	if *file != "" {
		samples, err = readWAV(*file)
		if err != nil {
			o.fail(err, "AUDIO")
			return 1
		}
	} else {
		recorder := audio.New(cfg.Audio.SampleRate, cfg.Audio.Device)
		if err := recorder.Start(); err != nil {
			o.fail(err, "AUDIO")
			return 1
		}
		o.info("Grabando %.0fs — hablá normal…", *seconds)
		time.Sleep(time.Duration(*seconds * float64(time.Second)))
		samples = recorder.Stop()
		if len(samples) == 0 {
			o.fail(fmt.Errorf("no entró audio. %s", recorder.Error()), "AUDIO")
			return 1
		}
	}

	audioSeconds := float64(len(samples)) / float64(cfg.Audio.SampleRate)
	if !o.json {
		fmt.Fprintf(o.stdout, "\naudio: %.1fs\n\n%-10s %9s  texto\n", audioSeconds, "motor", "latencia")
	}

	report := BenchReport{AudioSeconds: round2(audioSeconds)}
	for _, name := range names {
		run := cfg
		run.STT.Engine = name
		engine, err := stt.Build(stt.OptionsFrom(run))
		if err != nil {
			report.Results = append(report.Results, BenchResult{Engine: name, Error: err.Error()})
			continue
		}
		result := BenchResult{Engine: name}
		if _, live := engine.(stt.LiveEngine); live {
			result.Error = "es un motor vivo: escucha el micrófono él mismo y no se puede medir contra una grabación"
		} else if err := engine.Load(); err != nil {
			result.Error = err.Error()
		} else {
			// Dos pasadas y me quedo con la segunda: la primera todavía paga
			// cachés frías y conexiones que recién se abren.
			_, _ = engine.Transcribe(samples, false)
			started := time.Now()
			text, err := engine.Transcribe(samples, false)
			result.Seconds = round2(time.Since(started).Seconds())
			result.Text = text
			if err != nil {
				result.Error = err.Error()
			}
		}
		engine.Close()
		report.Results = append(report.Results, result)
		if !o.json {
			detail := result.Text
			if result.Error != "" {
				detail = "— " + result.Error
			}
			fmt.Fprintf(o.stdout, "%-10s %8.2fs  %s\n", name, result.Seconds, detail)
		}
	}

	if o.json {
		_ = o.printJSON(report)
	}
	return 0
}

func splitList(raw string) []string {
	var out []string
	for _, item := range strings.Split(raw, ",") {
		if item = strings.TrimSpace(item); item != "" {
			out = append(out, item)
		}
	}
	return out
}

// readWAV lee un WAV mono de 16 bits, que es lo único que graba esta app.
func readWAV(path string) ([]float32, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	if len(raw) < 44 || string(raw[0:4]) != "RIFF" || string(raw[8:12]) != "WAVE" {
		return nil, fmt.Errorf("%s no es un WAV", path)
	}
	channels := binary.LittleEndian.Uint16(raw[22:])
	rate := binary.LittleEndian.Uint32(raw[24:])
	bits := binary.LittleEndian.Uint16(raw[34:])
	if channels != 1 || rate != 16000 || bits != 16 {
		return nil, fmt.Errorf("el wav tiene que ser mono 16 kHz de 16 bits (es %d canales, %d Hz, %d bits)",
			channels, rate, bits)
	}
	pcm := raw[44:]
	pcm = pcm[:len(pcm)&^1]
	out := make([]float32, len(pcm)/2)
	for i := range out {
		out[i] = float32(int16(binary.LittleEndian.Uint16(pcm[2*i:]))) / 32768.0
	}
	return out, nil
}
