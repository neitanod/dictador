package overlay

import (
	"os"
	"os/exec"
	"strings"
	"sync"

	"golang.org/x/image/font"
	"golang.org/x/image/font/opentype"
)

// La fuente del sistema se busca con fc-match, que es lo que usa todo el
// escritorio para responder "¿y cuál es la sans-serif de esta máquina?". Sin
// eso habría que adivinar una ruta, y el texto se vería distinto en cada
// distribución.
var fallbackFonts = []string{
	"/usr/share/fonts/truetype/dejavu/DejaVuSans.ttf",
	"/usr/share/fonts/TTF/DejaVuSans.ttf",
	"/usr/share/fonts/truetype/liberation/LiberationSans-Regular.ttf",
	"/usr/share/fonts/noto/NotoSans-Regular.ttf",
}

var (
	fontOnce sync.Once
	fontPath string
)

// FontFile es el TTF que va a usar el overlay, o "" si no hay ninguno.
func FontFile() string {
	fontOnce.Do(func() {
		for _, pattern := range []string{"sans-serif", "sans"} {
			out, err := exec.Command("fc-match", "--format=%{file}", pattern).Output()
			if err == nil {
				candidate := strings.TrimSpace(string(out))
				// Las OpenType con curvas CFF no las dibuja x/image; si aparece
				// una, mejor caer a una TrueType conocida que no dibujar nada.
				if strings.HasSuffix(strings.ToLower(candidate), ".ttf") && exists(candidate) {
					fontPath = candidate
					return
				}
			}
		}
		for _, candidate := range fallbackFonts {
			if exists(candidate) {
				fontPath = candidate
				return
			}
		}
	})
	return fontPath
}

func exists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}

// faces son las dos tipografías del overlay: la del texto y la del estado.
type faces struct {
	body  font.Face
	small font.Face
}

// loadFaces abre el TTF en los dos tamaños que usa el overlay.
//
// El tamaño va en puntos y el DPI es el de la pantalla, igual que en cualquier
// toolkit: con los 72 DPI que uno pone sin pensar, un punto se dibuja como un
// píxel y la letra sale un 25% más chica que en el resto del escritorio.
func loadFaces(size int, dpi float64) (*faces, error) {
	path := FontFile()
	if path == "" {
		return nil, errNoFont
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	parsed, err := opentype.Parse(raw)
	if err != nil {
		return nil, err
	}
	if dpi <= 0 {
		dpi = 96
	}
	body, err := opentype.NewFace(parsed, &opentype.FaceOptions{
		Size: float64(size), DPI: dpi, Hinting: font.HintingFull,
	})
	if err != nil {
		return nil, err
	}
	smallSize := size - 7
	if smallSize < 8 {
		smallSize = 8
	}
	small, err := opentype.NewFace(parsed, &opentype.FaceOptions{
		Size: float64(smallSize), DPI: dpi, Hinting: font.HintingFull,
	})
	if err != nil {
		return nil, err
	}
	return &faces{body: body, small: small}, nil
}

func (f *faces) close() {
	if f == nil {
		return
	}
	if f.body != nil {
		_ = f.body.Close()
	}
	if f.small != nil {
		_ = f.small.Close()
	}
}
