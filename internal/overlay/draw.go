package overlay

import (
	"errors"
	"image"
	"image/color"
	"image/draw"
	"math"
	"strings"

	"golang.org/x/image/font"
	"golang.org/x/image/math/fixed"
	"golang.org/x/image/vector"
)

var errNoFont = errors.New("no encontré ninguna fuente TrueType para dibujar el overlay")

// Los colores son los mismos que tenía el overlay de Qt: la ventanita se
// reconoce por cómo se ve, y cambiarla de color sería cambiar de app.
var (
	colorBG     = color.NRGBA{18, 18, 22, 235}
	colorBorder = color.NRGBA{90, 90, 110, 200}
	colorText   = color.NRGBA{238, 238, 242, 255}
	colorDim    = color.NRGBA{150, 150, 165, 255}
	colorAccent = color.NRGBA{120, 200, 140, 255}
	colorBusy   = color.NRGBA{230, 190, 90, 255}
	colorError  = color.NRGBA{230, 110, 110, 255}
	colorTrack  = color.NRGBA{60, 60, 72, 255}
)

const (
	pad    = 18
	radius = 16
)

// frame es lo que hay que dibujar en un momento dado.
type frame struct {
	state   string // listening | thinking | done | error
	label   string
	text    string
	level   float64
	elapsed float64
	hover   bool
	width   int
}

// accent es el color del punto de estado y de la barra.
func (f frame) accent() color.NRGBA {
	switch f.state {
	case "thinking":
		return colorBusy
	case "error":
		return colorError
	default:
		return colorAccent
	}
}

// render dibuja el cuadro entero y devuelve la imagen lista para mandar a X.
func render(f frame, fc *faces) *image.RGBA {
	body := f.text
	if body == "" {
		// El placeholder no se repite: cuando dice lo mismo que la etiqueta de
		// arriba, el cuerpo se queda vacío y la ventanita respira.
		if candidate := placeholder(f.state); candidate != f.label {
			body = candidate
		}
	}
	lines := wrap(body, fc.body, f.width-2*pad)
	height := neededHeight(lines, fc)

	img := image.NewRGBA(image.Rect(0, 0, f.width, height))
	roundedRect(img, 0.5, 0.5, float64(f.width)-1, float64(height)-1, radius, colorBG, colorBorder, 1.4)

	accent := f.accent()

	// Punto de estado.
	circle(img, pad+4.5, pad+2.5, 4.5, accent)

	// Etiqueta. El atajo a la configuración se descubre pasando el mouse por
	// encima: anunciarlo siempre sería un cartel fijo tapando el estado.
	label := f.label
	if f.hover {
		label = "Click para configurar"
	}
	if label == "" {
		label = placeholder(f.state)
	}
	if label == "" {
		label = "Dictador"
	}
	drawString(img, fc.small, accent, pad+16, pad+7, label)

	if f.state == "listening" {
		// Contador de segundos a la derecha.
		secs := formatSeconds(f.elapsed)
		w := measure(fc.small, secs)
		drawString(img, fc.small, colorDim, f.width-pad-w, pad+7, secs)

		// Barra de nivel.
		barW := float64(f.width - 2*pad)
		y := float64(pad + 16)
		roundedRect(img, pad, y, barW, 3, 1.5, colorTrack, color.NRGBA{}, 0)
		filled := barW * math.Max(0.02, math.Min(1, f.level))
		roundedRect(img, pad, y, filled, 3, 1.5, accent, color.NRGBA{}, 0)
	}

	// Cuerpo del texto.
	textColor := colorText
	if f.text == "" {
		textColor = colorDim
	}
	lineHeight := fc.body.Metrics().Height.Ceil()
	y := pad + 26 + fc.body.Metrics().Ascent.Ceil()
	for _, line := range lines {
		drawString(img, fc.body, textColor, pad, y, line)
		y += lineHeight
	}
	return img
}

func placeholder(state string) string {
	switch state {
	case "listening":
		return "Escuchando…"
	case "thinking":
		return "Transcribiendo…"
	}
	return ""
}

func formatSeconds(seconds float64) string {
	// Redondear antes de partir: con truncado, 5,1 s se muestra como "5.0s"
	// porque el float más cercano a 5,1 es apenas menor.
	tenths := int(math.Round(seconds * 10))
	return itoa(tenths/10) + "." + itoa(tenths%10) + "s"
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var digits []byte
	for n > 0 {
		digits = append([]byte{byte('0' + n%10)}, digits...)
		n /= 10
	}
	return string(digits)
}

func neededHeight(lines []string, fc *faces) int {
	lineHeight := fc.body.Metrics().Height.Ceil()
	height := pad + 26 + len(lines)*lineHeight + pad
	if height < 96 {
		height = 96
	}
	return height
}

// wrap corta el texto en líneas que entren en el ancho dado.
func wrap(text string, face font.Face, width int) []string {
	words := strings.Fields(text)
	if len(words) == 0 {
		return []string{""}
	}
	var lines []string
	current := words[0]
	for _, word := range words[1:] {
		candidate := current + " " + word
		if measure(face, candidate) <= width {
			current = candidate
			continue
		}
		lines = append(lines, current)
		current = word
	}
	return append(lines, current)
}

func measure(face font.Face, text string) int {
	return font.MeasureString(face, text).Ceil()
}

func drawString(dst *image.RGBA, face font.Face, c color.NRGBA, x, y int, text string) {
	drawer := &font.Drawer{
		Dst:  dst,
		Src:  image.NewUniform(c),
		Face: face,
		Dot:  fixed.P(x, y),
	}
	drawer.DrawString(text)
}

// roundedRect pinta un rectángulo redondeado con relleno y, si el borde tiene
// alfa, una línea alrededor.
func roundedRect(dst *image.RGBA, x, y, w, h, r float64, fill, stroke color.NRGBA, strokeWidth float64) {
	if w <= 0 || h <= 0 {
		return
	}
	if r > w/2 {
		r = w / 2
	}
	if r > h/2 {
		r = h / 2
	}
	if fill.A > 0 {
		ras := vector.NewRasterizer(dst.Bounds().Dx(), dst.Bounds().Dy())
		pathRoundedRect(ras, x, y, w, h, r)
		ras.Draw(dst, dst.Bounds(), image.NewUniform(fill), image.Point{})
	}
	if stroke.A > 0 && strokeWidth > 0 {
		// El borde se dibuja como la diferencia entre dos rectángulos: el de
		// afuera pintado del color del borde, y el de adentro devuelto al
		// relleno. Es más simple que rasterizar un trazo y da el mismo píxel.
		outer := vector.NewRasterizer(dst.Bounds().Dx(), dst.Bounds().Dy())
		pathRoundedRect(outer, x, y, w, h, r)
		mask := image.NewAlpha(dst.Bounds())
		outer.Draw(mask, mask.Bounds(), image.NewUniform(color.Alpha{255}), image.Point{})

		inner := vector.NewRasterizer(dst.Bounds().Dx(), dst.Bounds().Dy())
		pathRoundedRect(inner, x+strokeWidth, y+strokeWidth,
			w-2*strokeWidth, h-2*strokeWidth, math.Max(0, r-strokeWidth))
		innerMask := image.NewAlpha(dst.Bounds())
		inner.Draw(innerMask, innerMask.Bounds(), image.NewUniform(color.Alpha{255}), image.Point{})

		for i := range mask.Pix {
			if v := int(mask.Pix[i]) - int(innerMask.Pix[i]); v > 0 {
				mask.Pix[i] = uint8(v)
			} else {
				mask.Pix[i] = 0
			}
		}
		draw.DrawMask(dst, dst.Bounds(), image.NewUniform(stroke), image.Point{},
			mask, image.Point{}, draw.Over)
	}
}

func pathRoundedRect(ras *vector.Rasterizer, x, y, w, h, r float64) {
	fx, fy := float32(x), float32(y)
	fw, fh, fr := float32(w), float32(h), float32(r)
	// El factor de las curvas de Bézier que mejor aproxima un cuarto de círculo.
	const k = 0.5523
	c := fr * k

	ras.MoveTo(fx+fr, fy)
	ras.LineTo(fx+fw-fr, fy)
	ras.CubeTo(fx+fw-fr+c, fy, fx+fw, fy+fr-c, fx+fw, fy+fr)
	ras.LineTo(fx+fw, fy+fh-fr)
	ras.CubeTo(fx+fw, fy+fh-fr+c, fx+fw-fr+c, fy+fh, fx+fw-fr, fy+fh)
	ras.LineTo(fx+fr, fy+fh)
	ras.CubeTo(fx+fr-c, fy+fh, fx, fy+fh-fr+c, fx, fy+fh-fr)
	ras.LineTo(fx, fy+fr)
	ras.CubeTo(fx, fy+fr-c, fx+fr-c, fy, fx+fr, fy)
	ras.ClosePath()
}

func circle(dst *image.RGBA, cx, cy, r float64, c color.NRGBA) {
	roundedRect(dst, cx-r, cy-r, 2*r, 2*r, r, c, color.NRGBA{}, 0)
}
