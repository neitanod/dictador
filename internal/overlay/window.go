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
//
// Puede haber más de una ventana a la vez: con `screen = "all"` aparece la
// misma ventanita en cada pantalla, y para eso cada una necesita su propia
// ventana X con su propio pixmap.
type Window struct {
	conn     *x11.Conn
	faces    *faces
	config   config.Overlay
	visual   xproto.Visualid
	depth    byte
	colormap xproto.Colormap
	metrics  metrics
	// scale lleva los tamaños escritos para 96 DPI a los de esta pantalla.
	scale float64

	mu       sync.Mutex
	current  frame
	visible  bool
	hideAt   time.Time
	screen   string
	position string
	surfaces []*surface

	clicked chan struct{}
	done    chan struct{}
	redraw  chan struct{}
	once    sync.Once
}

// surface es la ventanita en una pantalla.
type surface struct {
	win xproto.Window
	gc  xproto.Gcontext
	// buffer es el pixmap donde se arma el cuadro antes de mostrarlo: pintar
	// directo sobre la ventana deja ver cuadros a medio hacer, y el overlay se
	// redibuja doce veces por segundo mientras hablás.
	buffer     xproto.Pixmap
	bufferSize image.Point
	size       image.Point
	origin     image.Point
	monitor    x11.Monitor
	mapped     bool
}

// NewWindow abre el overlay. Devuelve error si esta sesión no puede dibujarlo
// —sin visual de 32 bits o sin fuentes—, y ahí el daemon cae a la notificación.
func NewWindow(cfg config.Overlay) (*Window, error) {
	conn, err := x11.Open()
	if err != nil {
		return nil, err
	}
	visual, depth, ok := conn.ARGBVisual()
	if !ok {
		conn.Close()
		return nil, fmt.Errorf("esta pantalla no tiene un visual de 32 bits: sin transparencia no hay overlay")
	}
	// El tamaño de la letra va en puntos, así que hay que saber a qué DPI
	// dibuja esta pantalla antes de abrir la fuente.
	dpi := conn.DPI()
	fc, err := loadFaces(cfg.FontSize, dpi)
	if err != nil {
		conn.Close()
		return nil, err
	}

	w := &Window{
		conn:     conn,
		faces:    fc,
		config:   cfg,
		visual:   visual,
		depth:    depth,
		metrics:  newMetrics(dpi),
		scale:    dpi / x11.DefaultDPI,
		screen:   cfg.Screen,
		position: cfg.Position,
		clicked:  make(chan struct{}, 4),
		done:     make(chan struct{}),
		redraw:   make(chan struct{}, 1),
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
	w.colormap = colormap

	// Una ventana de prueba: si el servidor no la acepta, mejor enterarse acá y
	// caer a las notificaciones que fallar en el primer dictado.
	probe, err := w.newSurface()
	if err != nil {
		w.fail()
		return nil, err
	}
	w.surfaces = []*surface{probe}

	go w.serve()
	go w.tick()
	return w, nil
}

func (w *Window) fail() {
	w.faces.close()
	w.conn.Close()
}

// newSurface crea una ventana X más su contexto gráfico.
func (w *Window) newSurface() (*surface, error) {
	win, err := xproto.NewWindowId(w.conn.X)
	if err != nil {
		return nil, err
	}
	// OverrideRedirect saca al gestor de ventanas del medio, y BorderPixel hace
	// falta porque el visual de 32 bits no hereda el del root.
	err = xproto.CreateWindowChecked(w.conn.X, w.depth, win, w.conn.Root,
		0, 0, 100, 100, 0, xproto.WindowClassInputOutput, w.visual,
		xproto.CwBackPixel|xproto.CwBorderPixel|xproto.CwOverrideRedirect|
			xproto.CwEventMask|xproto.CwColormap,
		[]uint32{
			0, // fondo transparente
			0,
			1, // override-redirect
			xproto.EventMaskExposure | xproto.EventMaskButtonPress |
				xproto.EventMaskEnterWindow | xproto.EventMaskLeaveWindow,
			uint32(w.colormap),
		}).Check()
	if err != nil {
		return nil, err
	}
	if err := w.declareType(win); err != nil {
		return nil, err
	}
	gc, err := xproto.NewGcontextId(w.conn.X)
	if err != nil {
		return nil, err
	}
	if err := xproto.CreateGCChecked(w.conn.X, gc, xproto.Drawable(win), 0, nil).Check(); err != nil {
		return nil, err
	}
	return &surface{win: win, gc: gc}, nil
}

func (s *surface) destroy(conn *x11.Conn) {
	if s.buffer != 0 {
		_ = xproto.FreePixmapChecked(conn.X, s.buffer).Check()
	}
	_ = xproto.FreeGCChecked(conn.X, s.gc).Check()
	_ = xproto.DestroyWindowChecked(conn.X, s.win).Check()
}

// declareType le dice al escritorio que esto es una notificación que va arriba
// de todo. Con override-redirect el gestor no lo lee, pero los compositores sí,
// y es lo que decide que no le pongan sombra ni animación de ventana.
func (w *Window) declareType(win xproto.Window) error {
	kind, err := w.conn.Atom("_NET_WM_WINDOW_TYPE")
	if err != nil {
		return err
	}
	notification, err := w.conn.Atom("_NET_WM_WINDOW_TYPE_NOTIFICATION")
	if err != nil {
		return err
	}
	if err := w.setAtomProperty(win, kind, notification); err != nil {
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
	return w.setAtomProperty(win, state, above)
}

func (w *Window) setAtomProperty(win xproto.Window, property, value xproto.Atom) error {
	data := []byte{byte(value), byte(value >> 8), byte(value >> 16), byte(value >> 24)}
	return xproto.ChangePropertyChecked(w.conn.X, xproto.PropModeReplace, win,
		property, xproto.AtomAtom, 32, 1, data).Check()
}

// Clicked avisa que le hicieron click, que es el atajo a la configuración.
func (w *Window) Clicked() <-chan struct{} { return w.clicked }

// SetPlacement cambia en caliente dónde aparece la ventanita.
func (w *Window) SetPlacement(screen, position string) {
	w.mu.Lock()
	if screen != "" {
		w.screen = screen
	}
	if ValidPosition(position) {
		w.position = position
	}
	w.mu.Unlock()
	w.wake()
}

// Placement es dónde está apareciendo ahora.
func (w *Window) Placement() (screen, position string) {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.screen, w.position
}

// ---- la interfaz UI ------------------------------------------------------

func (w *Window) BeginListening(hint string) {
	if hint == "" {
		hint = "Escuchando…"
	}
	w.mu.Lock()
	w.current = frame{state: "listening", label: hint, hover: w.current.hover}
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
	listening := w.current.state == "listening"
	w.current.level, w.current.elapsed = level, elapsed
	w.mu.Unlock()
	if listening {
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
	w.current = frame{state: "done", label: status, text: text, hover: w.current.hover}
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
	w.current = frame{state: "error", label: "Error", text: message, hover: w.current.hover}
	w.hideAt = time.Now().Add(4 * time.Second)
	w.mu.Unlock()
	w.show()
}

func (w *Window) Dismiss() {
	w.mu.Lock()
	w.hideAt = time.Time{}
	w.visible = false
	surfaces := append([]*surface(nil), w.surfaces...)
	w.mu.Unlock()
	for _, s := range surfaces {
		if s.mapped {
			s.mapped = false
			_ = xproto.UnmapWindowChecked(w.conn.X, s.win).Check()
		}
	}
}

func (w *Window) Close() {
	w.once.Do(func() {
		close(w.done)
		w.mu.Lock()
		surfaces := w.surfaces
		w.surfaces = nil
		w.mu.Unlock()
		for _, s := range surfaces {
			s.destroy(w.conn)
		}
		w.faces.close()
		w.conn.Close()
	})
}

// ---- dibujo --------------------------------------------------------------

// widthFor es el ancho de la ventanita en una pantalla dada: el configurado,
// salvo que no entre.
func (w *Window) widthFor(monitor x11.Monitor) int {
	width := w.config.Width
	if width <= 0 {
		width = 780
	}
	// El ancho también está escrito para 96 DPI: en una pantalla más densa la
	// ventanita tiene que crecer igual que la letra, o entra la mitad del texto.
	width = int(float64(width)*w.scale + 0.5)
	if max := monitor.Width - 60; max > 120 && width > max {
		width = max
	}
	return width
}

// show deja la ventanita en pantalla y pide un redibujo.
func (w *Window) show() {
	w.mu.Lock()
	w.visible = true
	w.mu.Unlock()
	w.wake()
}

func (w *Window) wake() {
	select {
	case w.redraw <- struct{}{}:
	default:
	}
}

// tick redibuja mientras haya algo en pantalla, y la esconde cuando se cumple
// el plazo. Es el reemplazo de los QTimer de la versión Qt.
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
			visible, hideAt := w.visible, w.hideAt
			w.mu.Unlock()
			if !visible {
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
	current, visible := w.current, w.visible
	screen, position := w.screen, w.position
	w.mu.Unlock()
	if !visible {
		return
	}

	w.syncSurfaces(targets(w.conn, screen))

	w.mu.Lock()
	surfaces := append([]*surface(nil), w.surfaces...)
	w.mu.Unlock()

	// Con pantallas de distinto ancho la ventanita se dibuja distinta en cada
	// una, así que el cuadro se cachea por ancho en vez de rasterizarse de nuevo
	// para cada pantalla.
	frames := map[int]*image.RGBA{}
	for _, s := range surfaces {
		width := w.widthFor(s.monitor)
		img, ok := frames[width]
		if !ok {
			shot := current
			shot.width = width
			img = render(shot, w.faces, w.metrics)
			frames[width] = img
		}
		w.paintSurface(s, img, position)
	}
}

// syncSurfaces deja una ventana por pantalla de destino.
func (w *Window) syncSurfaces(monitors []x11.Monitor) {
	if len(monitors) == 0 {
		return
	}
	w.mu.Lock()
	surfaces := w.surfaces
	w.mu.Unlock()

	for len(surfaces) < len(monitors) {
		s, err := w.newSurface()
		if err != nil {
			break
		}
		surfaces = append(surfaces, s)
	}
	if len(surfaces) > len(monitors) {
		for _, extra := range surfaces[len(monitors):] {
			extra.destroy(w.conn)
		}
		surfaces = surfaces[:len(monitors)]
	}
	for i := range surfaces {
		surfaces[i].monitor = monitors[i]
	}

	w.mu.Lock()
	w.surfaces = surfaces
	w.mu.Unlock()
}

func (w *Window) paintSurface(s *surface, img *image.RGBA, position string) {
	size := img.Bounds().Size()
	x, y := place(s.monitor, size, position, w.config.Margin)

	if size != s.size || image.Pt(x, y) != s.origin {
		s.size, s.origin = size, image.Pt(x, y)
		_ = xproto.ConfigureWindowChecked(w.conn.X, s.win,
			xproto.ConfigWindowX|xproto.ConfigWindowY|
				xproto.ConfigWindowWidth|xproto.ConfigWindowHeight,
			[]uint32{uint32(int32(x)), uint32(int32(y)),
				uint32(size.X), uint32(size.Y)}).Check()
	}
	if err := w.ensureBuffer(s, size); err != nil {
		return
	}
	w.put(s, img)

	if !s.mapped {
		s.mapped = true
		_ = xproto.MapWindowChecked(w.conn.X, s.win).Check()
		// Con override-redirect nadie más la sube: se sube ella.
		_ = xproto.ConfigureWindowChecked(w.conn.X, s.win,
			xproto.ConfigWindowStackMode, []uint32{xproto.StackModeAbove}).Check()
	}
	// Recién ahora el cuadro entero aparece, de una.
	_ = xproto.CopyAreaChecked(w.conn.X, xproto.Drawable(s.buffer),
		xproto.Drawable(s.win), s.gc, 0, 0, 0, 0,
		uint16(size.X), uint16(size.Y)).Check()
}

// ensureBuffer deja un pixmap del tamaño del cuadro.
func (w *Window) ensureBuffer(s *surface, size image.Point) error {
	if s.buffer != 0 && s.bufferSize == size {
		return nil
	}
	if s.buffer != 0 {
		_ = xproto.FreePixmapChecked(w.conn.X, s.buffer).Check()
		s.buffer = 0
	}
	pixmap, err := xproto.NewPixmapId(w.conn.X)
	if err != nil {
		return err
	}
	if err := xproto.CreatePixmapChecked(w.conn.X, w.depth, pixmap,
		xproto.Drawable(s.win), uint16(size.X), uint16(size.Y)).Check(); err != nil {
		return err
	}
	s.buffer, s.bufferSize = pixmap, size
	return nil
}

// put manda la imagen al pixmap de trabajo.
//
// Va en bandas de filas porque un PutImage entero no entra en un pedido de X:
// el máximo son unos 256 KB y el overlay pasa fácil de los 300 KB.
func (w *Window) put(s *surface, img *image.RGBA) {
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
			xproto.Drawable(s.buffer), s.gc,
			uint16(width), uint16(rows), 0, int16(top), 0, w.depth, data).Check()
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
			w.setHover(true)
		case xproto.LeaveNotifyEvent:
			w.setHover(false)
		case xproto.ButtonPressEvent:
			// El click llega igual sin aceptar foco: esa bandera le pide al
			// gestor que no le dé el teclado, y el mouse no pasa por ahí.
			w.setHover(false)
			select {
			case w.clicked <- struct{}{}:
			default:
			}
		}
	}
}

func (w *Window) setHover(hover bool) {
	w.mu.Lock()
	w.current.hover = hover
	w.mu.Unlock()
	w.wake()
}
