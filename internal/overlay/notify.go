package overlay

import (
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Notify muestra el estado del dictado como una notificación del escritorio.
//
// Es la ventanita de la fase 1: no queda linda como la de Qt, pero se actualiza
// en el lugar mientras hablás porque `notify-send -p` devuelve un id y `-r` lo
// reemplaza. Sin eso serían veinte notificaciones apiladas por dictado.
//
// El medidor de nivel no se dibuja: mandarle veinte notificaciones por segundo
// al escritorio sería peor que no mostrar nada.
type Notify struct {
	mu       sync.Mutex
	id       string
	lastText string
	lastAt   time.Time
	// throttle es cada cuánto se deja actualizar el texto en vivo.
	throttle time.Duration
}

// NewNotify arma la ventanita por notificaciones.
func NewNotify() *Notify { return &Notify{throttle: 400 * time.Millisecond} }

// NotifyAvailable dice si el escritorio tiene notify-send.
func NotifyAvailable() bool {
	_, err := exec.LookPath("notify-send")
	return err == nil
}

func (n *Notify) BeginListening(hint string) {
	if hint == "" {
		hint = "Escuchando…"
	}
	n.mu.Lock()
	n.lastText, n.lastAt = "", time.Time{}
	n.mu.Unlock()
	n.show(hint, "", 30*time.Second, "audio-input-microphone")
}

func (n *Notify) SetPartial(text string) {
	if text == "" {
		return
	}
	n.mu.Lock()
	fresh := text != n.lastText && time.Since(n.lastAt) >= n.throttle
	if fresh {
		n.lastText, n.lastAt = text, time.Now()
	}
	n.mu.Unlock()
	if fresh {
		n.show("Escuchando…", text, 30*time.Second, "audio-input-microphone")
	}
}

// SetMeter no hace nada: ver el comentario del tipo.
func (n *Notify) SetMeter(float64, float64) {}

func (n *Notify) SetThinking(status string) {
	n.show(status, "", 30*time.Second, "audio-input-microphone")
}

func (n *Notify) SetDone(text, status string, hideAfter time.Duration) {
	if hideAfter <= 0 {
		hideAfter = 4 * time.Second
	}
	n.show(status, text, hideAfter, "dialog-information")
}

func (n *Notify) SetError(message string) {
	n.show("Dictador", message, 6*time.Second, "dialog-error")
}

// Dismiss cierra la notificación que esté en pantalla.
func (n *Notify) Dismiss() {
	n.mu.Lock()
	id := n.id
	n.mu.Unlock()
	if id == "" {
		return
	}
	// Una notificación de 1 ms es la forma portable de cerrar la anterior:
	// gdbus/notify-send no exponen un "cerrala" que ande en todos lados.
	_ = exec.Command("notify-send", "-r", id, "-t", "1", "-u", "low", " ").Run()
}

func (n *Notify) Close() { n.Dismiss() }

func (n *Notify) show(summary, body string, timeout time.Duration, icon string) {
	n.mu.Lock()
	id := n.id
	n.mu.Unlock()

	args := []string{
		"-p",
		"-a", "Dictador",
		"-i", icon,
		"-t", strconv.Itoa(int(timeout.Milliseconds())),
		"-h", "string:x-canonical-private-synchronous:dictador",
	}
	if id != "" {
		args = append(args, "-r", id)
	}
	args = append(args, summary)
	if body != "" {
		args = append(args, body)
	}

	out, err := exec.Command("notify-send", args...).Output()
	if err != nil {
		return
	}
	if fresh := strings.TrimSpace(string(out)); fresh != "" {
		n.mu.Lock()
		n.id = fresh
		n.mu.Unlock()
	}
}
