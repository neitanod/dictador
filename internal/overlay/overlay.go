// Package overlay es la ventanita que muestra lo que vas diciendo.
//
// En la fase 1 la implementación es una notificación del escritorio, que se
// actualiza en el lugar mientras hablás. La fase 2 va a traer la ventana
// dibujada a mano en X11 —translúcida, sin foco, siempre arriba— detrás de esta
// misma interfaz, así el daemon no se entera del cambio.
package overlay

import "time"

// UI es lo que el daemon le pide a la ventanita.
type UI interface {
	// BeginListening: arrancó el dictado.
	BeginListening(hint string)
	// SetPartial: esto es lo que se viene entendiendo.
	SetPartial(text string)
	// SetMeter: nivel de audio en [0, 1] y segundos grabados.
	SetMeter(level, elapsed float64)
	// SetThinking: se soltó la tecla y se está transcribiendo.
	SetThinking(status string)
	// SetDone: el texto final, con lo que se hizo con él.
	SetDone(text, status string, hideAfter time.Duration)
	// SetError: algo salió mal y hay que decirlo.
	SetError(message string)
	// Dismiss: sacarla de la pantalla.
	Dismiss()
	// Close: soltar lo que tenga tomado.
	Close()
}

// Clickable la implementa la ventanita a la que se le puede hacer click, que
// es el atajo a la configuración. La notificación del escritorio no la
// implementa: ahí no hay dónde hacer click.
type Clickable interface {
	Clicked() <-chan struct{}
}

// Nop es la ventanita apagada: la que se usa con overlay.enabled = false.
type Nop struct{}

func (Nop) BeginListening(string)                 {}
func (Nop) SetPartial(string)                     {}
func (Nop) SetMeter(float64, float64)             {}
func (Nop) SetThinking(string)                    {}
func (Nop) SetDone(string, string, time.Duration) {}
func (Nop) SetError(string)                       {}
func (Nop) Dismiss()                              {}
func (Nop) Close()                                {}
