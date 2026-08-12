// Package audio captura el micrófono con parec (PipeWire/PulseAudio).
//
// Usamos el binario en vez de bindings porque parec ya está en cualquier Ubuntu
// con PipeWire, arranca en milisegundos y deja el audio en crudo listo para
// Whisper: 16 kHz, mono, s16le.
package audio

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"math"
	"os/exec"
	"strings"
	"sync"
	"time"
)

// chunkBytes son 100 ms a 16 kHz mono s16le.
const chunkBytes = 3200

// Recorder graba en background y deja el buffer disponible en cualquier momento.
type Recorder struct {
	sampleRate int
	device     string

	mu     sync.Mutex
	cmd    *exec.Cmd
	buf    bytes.Buffer
	level  float64
	stderr bytes.Buffer
	done   chan struct{}
}

// New arma un grabador. No abre nada hasta Start.
func New(sampleRate int, device string) *Recorder {
	if sampleRate <= 0 {
		sampleRate = 16000
	}
	return &Recorder{sampleRate: sampleRate, device: device}
}

// Available dice si parec está en el PATH.
func Available() bool {
	_, err := exec.LookPath("parec")
	return err == nil
}

// SampleRate es la frecuencia a la que graba.
func (r *Recorder) SampleRate() int { return r.sampleRate }

// Start abre el micrófono. Llamarlo dos veces seguidas no hace nada.
func (r *Recorder) Start() error {
	r.mu.Lock()
	if r.cmd != nil {
		r.mu.Unlock()
		return nil
	}
	r.mu.Unlock()

	if !Available() {
		return errors.New("falta `parec` (paquete pulseaudio-utils)")
	}
	args := []string{
		"--format=s16le",
		fmt.Sprintf("--rate=%d", r.sampleRate),
		"--channels=1",
		"--latency-msec=50",
	}
	if r.device != "" {
		args = append(args, "--device="+r.device)
	}
	cmd := exec.Command("parec", args...)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	var errBuf bytes.Buffer
	cmd.Stderr = &errBuf
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("no pude arrancar parec: %w", err)
	}

	r.mu.Lock()
	r.cmd = cmd
	r.buf.Reset()
	r.level = 0
	r.stderr.Reset()
	r.done = make(chan struct{})
	done := r.done
	r.mu.Unlock()

	go r.pump(stdout, &errBuf, done)
	return nil
}

func (r *Recorder) pump(stdout io.ReadCloser, errBuf *bytes.Buffer, done chan struct{}) {
	defer close(done)
	chunk := make([]byte, chunkBytes)
	for {
		n, err := io.ReadFull(stdout, chunk)
		if n > 0 {
			level := rms(chunk[:n&^1])
			r.mu.Lock()
			r.buf.Write(chunk[:n])
			// Suavizado para que la barra del overlay no tiemble.
			r.level = math.Max(level, r.level*0.6)
			r.mu.Unlock()
		}
		if err != nil {
			break
		}
	}
	r.mu.Lock()
	r.stderr.Write(errBuf.Bytes())
	r.mu.Unlock()
}

// rms es el nivel medio de un bloque de s16le, normalizado a [0, 1].
func rms(pcm []byte) float64 {
	if len(pcm) < 2 {
		return 0
	}
	var sum float64
	n := len(pcm) / 2
	for i := 0; i < n; i++ {
		s := float64(int16(uint16(pcm[2*i])|uint16(pcm[2*i+1])<<8)) / 32768.0
		sum += s * s
	}
	return math.Sqrt(sum / float64(n))
}

// Stop cierra el micrófono y devuelve todo lo grabado.
func (r *Recorder) Stop() []float32 {
	r.mu.Lock()
	cmd, done := r.cmd, r.done
	r.cmd = nil
	r.mu.Unlock()

	if cmd != nil && cmd.Process != nil {
		_ = cmd.Process.Signal(interruptSignal)
		waited := make(chan struct{})
		go func() { _ = cmd.Wait(); close(waited) }()
		select {
		case <-waited:
		case <-time.After(1500 * time.Millisecond):
			_ = cmd.Process.Kill()
			<-waited
		}
	}
	if done != nil {
		select {
		case <-done:
		case <-time.After(1500 * time.Millisecond):
		}
	}
	return r.Snapshot()
}

// Snapshot es todo lo grabado hasta ahora, como float32 en [-1, 1].
func (r *Recorder) Snapshot() []float32 {
	r.mu.Lock()
	raw := append([]byte(nil), r.buf.Bytes()...)
	r.mu.Unlock()
	return decode(raw)
}

func decode(raw []byte) []float32 {
	raw = raw[:len(raw)&^1]
	out := make([]float32, len(raw)/2)
	for i := range out {
		out[i] = float32(int16(uint16(raw[2*i])|uint16(raw[2*i+1])<<8)) / 32768.0
	}
	return out
}

// PCM devuelve lo grabado tal cual vino: s16le, listo para mandar por la red.
func (r *Recorder) PCM() []byte {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]byte(nil), r.buf.Bytes()...)
}

// Level es el nivel de audio suavizado para la barra, en [0, 1]. Leerlo lo hace
// decaer: si dejaste de hablar, la barra baja sola.
func (r *Recorder) Level() float64 {
	r.mu.Lock()
	level := r.level
	r.level *= 0.75
	r.mu.Unlock()
	return math.Min(1.0, level*4.0)
}

// Seconds es cuánto lleva grabado.
func (r *Recorder) Seconds() float64 {
	r.mu.Lock()
	defer r.mu.Unlock()
	return float64(r.buf.Len()) / 2 / float64(r.sampleRate)
}

// Error es lo que parec haya escrito en stderr, que es donde cuenta por qué no
// pudo abrir el micrófono.
func (r *Recorder) Error() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return strings.TrimSpace(r.stderr.String())
}

// Running dice si el micrófono está abierto.
func (r *Recorder) Running() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.cmd != nil
}
