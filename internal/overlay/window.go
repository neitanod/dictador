package overlay

import (
	"fmt"
	"image"
	"sync"
	"time"

	"github.com/jezek/xgb/xproto"
	"github.com/neitanod/dictador/internal/config"
	"github.com/neitanod/dictador/internal/x11"
)

// Window es el overlay dibujado a mano: translúcido, sin foco, siempre arriba.
//
// Sin foco es la parte que importa: si lo aceptara se llevaría el cursor del
// campo al que le estás dictando, que es justo lo que la app existe para
// evitar. Es override-redirect, así que el gestor de ventanas no lo toca — no
// lo mueve, no lo decora y no le da el teclado.
type Window struct {
	conn   *x11.Conn
	win    xproto.Window
	gc     xproto.Gcontext
	faces  *faces
	config config.Overlay

	mu      sync.Mutex
	current frame
	mapped  bool
	size    image.Point
	// buffer es el pixmap donde se arma el cuadro antes de mostrarlo: pintar
	// directo sobre la ventana deja ver cuadros a medio hacer, y el overlay se
	// redibuja doce veces por segundo mientras hablás.
	buffer     xproto.Pixmap
	bufferSize image.Point

	hideAt  time.Time
	clicked chan struct{}
	done    chan struct{}
	once    sync.Once
	redraw  chan struct{}
}

// NewWindow abre el overlay. Devuelve error si esta sesión no puede dibujarlo
// —sin visual de 32 bits o sin fuentes—, y ahí el daemon cae a la notificación.
func NewWindow(cfg config.Overlay) (*Window, error) {
	fc, err := loadFaces(cfg.FontSize)
	if err != nil {
		return nil, err
	}
	conn, err := x11.Open()
	if err != nil {
		fc.close()
		return nil, err
	}
	visual, depth, ok := conn.ARGBVisual()
	if !ok {
		conn.Close()
		fc.close()
		return nil, fmt.Errorf("esta pantalla no tiene un visual de 32 bits: sin transparencia no hay overlay")
	}

	w := &Window{
		conn:    conn,
		faces:   fc,
		config:  cfg,
		clicked: make(chan struct{}, 4),
		done:    make(chan struct{}),
		redraw:  make(chan struct{}, 1),
	}

	colormap, err := xproto.NewColormapId(conn.X)
	if err != nil {
		w.fail()
		return nil, err
	}
	if err := xproto.CreateColormapChecked(conn.X, xproto.ColormapAllocNone,
		colormap, conn.Root, visual).Check(); err != nil {
		w.fail()
		return nil, err
	}

	win, err := xproto.NewWindowId(conn.X)
	if err != nil {
		w.fail()
		return nil, err
	}
	// OverrideRedirect saca al gestor de ventanas del medio, y BorderPixel hace
	// falta porque el visual de 32 bits no hereda el del root.
	err = xproto.CreateWindowChecked(conn.X, depth, win, conn.Root,
		0, 0, 100, 100, 0, xproto.WindowClassInputOutput, visual,
		xproto.CwBackPixel|xproto.CwBorderPixel|xproto.CwOverrideRedirect|
			xproto.CwEventMask|xproto.CwColormap,
		[]uint32{
			0, // fondo transparente
			0,
			1, // override-redirect
			xproto.EventMaskExposure | xproto.EventMaskButtonPress |
				xproto.EventMaskEnterWindow | xproto.EventMaskLeaveWindow,
			uint32(colormap),
		}).Check()
	if err != nil {
		w.fail()
		return nil, err
	}
	w.win = win

	if err := w.declareType(); err != nil {
		w.fail()
		return nil, err
	}

	gc, err := xproto.NewGcontextId(conn.X)
	if err != nil {
		w.fail()
		return nil, err
	}
	if err := xproto.CreateGCChecked(conn.X, gc, xproto.Drawable(win), 0, nil).Check(); err != nil {
		w.fail()
		return nil, err
	}
	w.gc = gc

	go w.serve()
	go w.tick()
	return w, nil
}

func (w *Window) fail() {
	w.faces.close()
	w.conn.Close()
}

// declareType le dice al escritorio que esto es una notificación que va arriba
// de todo. Con override-redirect el gestor no lo lee, pero los compositores sí,
// y es lo que decide que no le pongan sombra ni animación de ventana.
func (w *Window) declareType() error {
	kind, err := w.conn.Atom("_NET_WM_WINDOW_TYPE")
	if err != nil {
		return err
	}
	notification, err := w.conn.Atom("_NET_WM_WINDOW_TYPE_NOTIFICATION")
	if err != nil {
		return err
	}
	if err := w.setAtomProperty(kind, notification); err != nil {
		return err
	}
	state, err := w.conn.Atom("_NET_WM_STATE")
	if err != nil {
		return err
	}
	above, err := w.conn.Atom("_NET_WM_STATE_ABOVE")
	if err != nil {
		return err
	}
	return w.setAtomProperty(state, above)
}

func (w *Window) setAtomProperty(property, value xproto.Atom) error {
	data := []byte{byte(value), byte(value >> 8), byte(value >> 16), byte(value >> 24)}
	return xproto.ChangePropertyChecked(w.conn.X, xproto.PropModeReplace, w.win,
		property, xproto.AtomAtom, 32, 1, data).Check()
}

// Clicked avisa que le hicieron click, que es el atajo a la configuración.
func (w *Window) Clicked() <-chan struct{} { return w.clicked }

// ---- la interfaz UI ------------------------------------------------------

func (w *Window) BeginListening(hint string) {
	if hint == "" {
		hint = "Escuchando…"
	}
	w.mu.Lock()
	w.current = frame{state: "listening", label: hint, width: w.width(), hover: w.current.hover}
	w.hideAt = time.Time{}
	w.mu.Unlock()
	w.show()
}

func (w *Window) SetPartial(text string) {
	if text == "" {
		return
	}
	w.mu.Lock()
	w.current.text = text
	w.mu.Unlock()
	w.show()
}

func (w *Window) SetMeter(level, elapsed float64) {
	w.mu.Lock()
	changed := w.current.state == "listening"
	w.current.level, w.current.elapsed = level, elapsed
	w.mu.Unlock()
	if changed {
		w.wake()
	}
}

func (w *Window) SetThinking(status string) {
	w.mu.Lock()
	w.current.state = "thinking"
	w.current.label = status
	w.mu.Unlock()
	w.show()
}

func (w *Window) SetDone(text, status string, hideAfter time.Duration) {
	w.mu.Lock()
	w.current = frame{state: "done", label: status, text: text, width: w.width(), hover: w.current.hover}
	if hideAfter < 0 {
		hideAfter = time.Duration(w.config.HideDelayMs) * time.Millisecond
	}
	if hideAfter > 0 {
		w.hideAt = time.Now().Add(hideAfter)
	} else {
		w.hideAt = time.Time{}
	}
	w.mu.Unlock()
	w.show()
}

func (w *Window) SetError(message string) {
	w.mu.Lock()
	w.current = frame{state: "error", label: "Error", text: message, width: w.width(), hover: w.current.hover}
	w.hideAt = time.Now().Add(4 * time.Second)
	w.mu.Unlock()
	w.show()
}

func (w *Window) Dismiss() {
	w.mu.Lock()
	w.hideAt = time.Time{}
	mapped := w.mapped
	w.mapped = false
	w.mu.Unlock()
	if mapped {
		_ = xproto.UnmapWindowChecked(w.conn.X, w.win).Check()
	}
}

func (w *Window) Close() {
	w.once.Do(func() {
		close(w.done)
		if w.buffer != 0 {
			_ = xproto.FreePixmapChecked(w.conn.X, w.buffer).Check()
		}
		w.faces.close()
		w.conn.Close()
	})
}

// ---- dibujo --------------------------------------------------------------

func (w *Window) width() int {
	if w.config.Width > 0 {
		return w.config.Width
	}
	return 780
}

// show mapea la ventana si hacía falta y pide un redibujo.
func (w *Window) show() {
	w.mu.Lock()
	mapped := w.mapped
	w.mapped = true
	w.mu.Unlock()
	if !mapped {
		_ = xproto.MapWindowChecked(w.conn.X, w.win).Check()
		// Con override-redirect nadie más la sube: se sube ella.
		_ = xproto.ConfigureWindowChecked(w.conn.X, w.win,
			xproto.ConfigWindowStackMode, []uint32{xproto.StackModeAbove}).Check()
	}
	w.wake()
}

func (w *Window) wake() {
	select {
	case w.redraw <- struct{}{}:
	default:
	}
}

// tick redibuja a 12 fps mientras haya algo en pantalla, y esconde la ventana
// cuando se cumple el plazo. Es el reemplazo del QTimer de la versión Qt.
func (w *Window) tick() {
	ticker := time.NewTicker(80 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-w.done:
			return
		case <-w.redraw:
			w.paint()
		case <-ticker.C:
			w.mu.Lock()
			mapped, hideAt := w.mapped, w.hideAt
			w.mu.Unlock()
			if !mapped {
				continue
			}
			if !hideAt.IsZero() && time.Now().After(hideAt) {
				w.Dismiss()
				continue
			}
			w.paint()
		}
	}
}

func (w *Window) paint() {
	w.mu.Lock()
	current := w.current
	mapped := w.mapped
	w.mu.Unlock()
	if !mapped {
		return
	}
	if current.width == 0 {
		current.width = w.width()
	}

	img := render(current, w.faces)
	size := img.Bounds().Size()

	w.mu.Lock()
	resized := size != w.size
	w.size = size
	w.mu.Unlock()

	if resized {
		x, y := w.position(size)
		_ = xproto.ConfigureWindowChecked(w.conn.X, w.win,
			xproto.ConfigWindowX|xproto.ConfigWindowY|
				xproto.ConfigWindowWidth|xproto.ConfigWindowHeight,
			[]uint32{uint32(int32(x)), uint32(int32(y)),
				uint32(size.X), uint32(size.Y)}).Check()
	}
	if err := w.ensureBuffer(size); err != nil {
		return
	}
	w.put(img)
	// Recién ahora el cuadro entero aparece, de una.
	_ = xproto.CopyAreaChecked(w.conn.X, xproto.Drawable(w.buffer),
		xproto.Drawable(w.win), w.gc, 0, 0, 0, 0,
		uint16(size.X), uint16(size.Y)).Check()
}

// ensureBuffer deja un pixmap del tamaño del cuadro.
func (w *Window) ensureBuffer(size image.Point) error {
	w.mu.Lock()
	current := w.bufferSize
	buffer := w.buffer
	w.mu.Unlock()
	if buffer != 0 && current == size {
		return nil
	}
	if buffer != 0 {
		_ = xproto.FreePixmapChecked(w.conn.X, buffer).Check()
	}
	pixmap, err := xproto.NewPixmapId(w.conn.X)
	if err != nil {
		return err
	}
	if err := xproto.CreatePixmapChecked(w.conn.X, 32, pixmap,
		xproto.Drawable(w.win), uint16(size.X), uint16(size.Y)).Check(); err != nil {
		return err
	}
	w.mu.Lock()
	w.buffer, w.bufferSize = pixmap, size
	w.mu.Unlock()
	return nil
}

// position calcula dónde va la ventanita según la configuración.
func (w *Window) position(size image.Point) (int, int) {
	screenW := int(w.conn.Screen.WidthInPixels)
	screenH := int(w.conn.Screen.HeightInPixels)
	margin := w.config.Margin
	if margin <= 0 {
		margin = 90
	}
	x := (screenW - size.X) / 2
	var y int
	switch w.config.Position {
	case "top-center":
		y = margin
	case "center":
		y = (screenH - size.Y) / 2
	default:
		y = screenH - size.Y - margin
	}
	if x < 0 {
		x = 0
	}
	if y < 0 {
		y = 0
	}
	return x, y
}

// put manda la imagen al pixmap de trabajo.
//
// Va en bandas de filas porque un PutImage entero no entra en un pedido de X:
// el máximo son unos 256 KB y el overlay pasa fácil de los 300 KB.
func (w *Window) put(img *image.RGBA) {
	bounds := img.Bounds()
	width, height := bounds.Dx(), bounds.Dy()
	rowBytes := width * 4

	maxRequest := int(w.conn.Setup.MaximumRequestLength)*4 - 64
	rowsPerBand := maxRequest / rowBytes
	if rowsPerBand < 1 {
		rowsPerBand = 1
	}

	for top := 0; top < height; top += rowsPerBand {
		rows := rowsPerBand
		if top+rows > height {
			rows = height - top
		}
		data := make([]byte, rows*rowBytes)
		for y := 0; y < rows; y++ {
			src := img.PixOffset(0, top+y)
			dst := y * rowBytes
			for x := 0; x < width; x++ {
				// image.RGBA ya viene premultiplicado, que es lo que espera un
				// visual ARGB32; lo único que cambia es el orden: X quiere BGRA.
				r := img.Pix[src+4*x+0]
				g := img.Pix[src+4*x+1]
				b := img.Pix[src+4*x+2]
				a := img.Pix[src+4*x+3]
				data[dst+4*x+0] = b
				data[dst+4*x+1] = g
				data[dst+4*x+2] = r
				data[dst+4*x+3] = a
			}
		}
		_ = xproto.PutImageChecked(w.conn.X, xproto.ImageFormatZPixmap,
			xproto.Drawable(w.buffer), w.gc,
			uint16(width), uint16(rows), 0, int16(top), 0, 32, data).Check()
	}
}

// ---- eventos -------------------------------------------------------------

func (w *Window) serve() {
	for {
		event, xerr := w.conn.X.WaitForEvent()
		select {
		case <-w.done:
			return
		default:
		}
		if xerr != nil {
			continue
		}
		if event == nil {
			return
		}
		switch event.(type) {
		case xproto.ExposeEvent:
			w.wake()
		case xproto.EnterNotifyEvent:
			w.mu.Lock()
			w.current.hover = true
			w.mu.Unlock()
			w.wake()
		case xproto.LeaveNotifyEvent:
			w.mu.Lock()
			w.current.hover = false
			w.mu.Unlock()
			w.wake()
		case xproto.ButtonPressEvent:
			// El click llega igual sin aceptar foco: esa bandera le pide al
			// gestor que no le dé el teclado, y el mouse no pasa por ahí.
			w.mu.Lock()
			w.current.hover = false
			w.mu.Unlock()
			select {
			case w.clicked <- struct{}{}:
			default:
			}
		}
	}
}
