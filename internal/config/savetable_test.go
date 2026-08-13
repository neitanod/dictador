package config

import (
	"strings"
	"testing"
)

func TestSaveTableSacaLoQueYaNoEsta(t *testing.T) {
	path := writeTemp(t, `[commands]
enabled = true

[commands.replacements]
"flecha" = "→"
"arroba" = "@"
`)
	if _, err := SaveTable(path, "commands.replacements", []Pair{{Key: "flecha", Value: "→"}}); err != nil {
		t.Fatalf("no pude guardar: %v", err)
	}
	out := read(t, path)
	if !strings.Contains(out, `"flecha" = "→"`) {
		t.Fatalf("se perdió la que quedaba:\n%s", out)
	}
	if strings.Contains(out, "arroba") {
		t.Fatalf("la que borré sigue en el archivo:\n%s", out)
	}

	// Y el archivo tiene que seguir siendo un TOML que se pueda leer.
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("dejé el config roto: %v", err)
	}
	if cfg.Commands.Replacements["flecha"] != "→" {
		t.Fatalf("esperaba la flecha, vinieron %v", cfg.Commands.Replacements)
	}
}

func TestSaveTableGuardaFrasesDeVariasPalabras(t *testing.T) {
	path := writeTemp(t, "[commands.replacements]\n")
	pairs := []Pair{
		{Key: "punto final", Value: "."},
		{Key: "palabra coma", Value: ","},
		{Key: "coma", Value: ""}, // apagar uno de fábrica
	}
	if _, err := SaveTable(path, "commands.replacements", pairs); err != nil {
		t.Fatalf("no pude guardar: %v", err)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("dejé el config roto: %v", err)
	}
	want := map[string]string{"punto final": ".", "palabra coma": ",", "coma": ""}
	for say, writes := range want {
		got, ok := cfg.Commands.Replacements[say]
		if !ok {
			t.Fatalf("falta %q en %v", say, cfg.Commands.Replacements)
		}
		if got != writes {
			t.Fatalf("%q escribe %q y esperaba %q", say, got, writes)
		}
	}
}

func TestSaveTableEscapaLoQueRompeElTOML(t *testing.T) {
	path := writeTemp(t, "[commands.replacements]\n")
	pairs := []Pair{{Key: `abre comillas`, Value: `"`}, {Key: `barra`, Value: `\`}}
	if _, err := SaveTable(path, "commands.replacements", pairs); err != nil {
		t.Fatalf("no pude guardar: %v", err)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("una comilla en el valor me rompió el archivo: %v", err)
	}
	if cfg.Commands.Replacements["abre comillas"] != `"` {
		t.Fatalf("la comilla no volvió entera: %v", cfg.Commands.Replacements)
	}
	if cfg.Commands.Replacements["barra"] != `\` {
		t.Fatalf("la barra invertida no volvió entera: %v", cfg.Commands.Replacements)
	}
}

func TestSaveTableDejaLosComentariosDeLaTabla(t *testing.T) {
	path := writeTemp(t, `[commands.replacements]
# "dos puntos" = ":"
# "flecha" = "→"
`)
	if _, err := SaveTable(path, "commands.replacements", []Pair{{Key: "flecha", Value: "→"}}); err != nil {
		t.Fatalf("no pude guardar: %v", err)
	}
	out := read(t, path)
	if !strings.Contains(out, `# "dos puntos" = ":"`) {
		t.Fatalf("me comí los comentarios que explican la tabla:\n%s", out)
	}
}

// El bloque de comentarios que le presenta a la sección siguiente arranca
// después de un renglón en blanco, y tiene que quedar pegado a ella: si las
// entradas se escribieran al final del cuerpo, el comentario terminaría
// hablando de la tabla equivocada.
func TestSaveTableNoSeMetePorEncimaDeLaSeccionSiguiente(t *testing.T) {
	path := writeTemp(t, `[commands.replacements]
"vieja" = "x"

# La ventanita que muestra lo que vas diciendo.
[overlay]
enabled = true
`)
	if _, err := SaveTable(path, "commands.replacements", []Pair{{Key: "nueva", Value: "y"}}); err != nil {
		t.Fatalf("no pude guardar: %v", err)
	}
	out := read(t, path)
	comment := strings.Index(out, "# La ventanita")
	entry := strings.Index(out, `"nueva"`)
	if entry < 0 || comment < 0 {
		t.Fatalf("falta algo en el archivo:\n%s", out)
	}
	if entry > comment {
		t.Fatalf("la entrada nueva se metió abajo del comentario de [overlay]:\n%s", out)
	}
	if _, err := Load(path); err != nil {
		t.Fatalf("dejé el config roto: %v", err)
	}
}

func TestSaveTableCreaLaTablaSiNoEstaba(t *testing.T) {
	path := writeTemp(t, "[commands]\nenabled = true\n")
	if _, err := SaveTable(path, "commands.replacements", []Pair{{Key: "punto final", Value: "."}}); err != nil {
		t.Fatalf("no pude guardar: %v", err)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("dejé el config roto: %v", err)
	}
	if cfg.Commands.Replacements["punto final"] != "." {
		t.Fatalf("no quedó guardado: %v", cfg.Commands.Replacements)
	}
	if !cfg.Commands.Enabled {
		t.Fatal("me llevé puesto el enabled que ya estaba")
	}
}

func TestSaveTableVaciaLaTabla(t *testing.T) {
	path := writeTemp(t, "[commands.replacements]\n\"flecha\" = \"→\"\n")
	if _, err := SaveTable(path, "commands.replacements", nil); err != nil {
		t.Fatalf("no pude guardar: %v", err)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("dejé el config roto: %v", err)
	}
	if len(cfg.Commands.Replacements) != 0 {
		t.Fatalf("esperaba la tabla vacía, quedó %v", cfg.Commands.Replacements)
	}
}
