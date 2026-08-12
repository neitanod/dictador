package history

import (
	"os"
	"strings"
	"testing"
)

func TestAppendYLoadVanYVuelven(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())

	for _, text := range []string{"uno", "dos", "tres"} {
		if err := Append(Entry{Text: text, Action: "paste", Target: "konsole"}); err != nil {
			t.Fatal(err)
		}
	}
	entries, err := Load(0)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 3 {
		t.Fatalf("guardó %d dictados", len(entries))
	}
	if entries[0].Text != "uno" || entries[2].Text != "tres" {
		t.Errorf("el orden es el de los dictados: %+v", entries)
	}
	if entries[0].At == "" {
		t.Error("le tendría que poner la hora solo")
	}
}

func TestLoadDevuelveLosUltimos(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	for _, text := range []string{"uno", "dos", "tres"} {
		_ = Append(Entry{Text: text})
	}
	entries, err := Load(2)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 2 || entries[0].Text != "dos" {
		t.Errorf("los últimos dos son «dos» y «tres»: %+v", entries)
	}
}

func TestLoadSaltaLasLineasRotas(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	_ = Append(Entry{Text: "bueno"})
	// Una escritura a medias no puede tapar el resto del historial.
	file, err := os.OpenFile(Path(), os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = file.WriteString("{esto no es json\n")
	file.Close()
	_ = Append(Entry{Text: "otro bueno"})

	entries, err := Load(0)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 2 {
		t.Errorf("tendría que leer los dos buenos: %+v", entries)
	}
}

func TestLoadSinArchivoDiceQueNoExiste(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	if _, err := Load(0); !os.IsNotExist(err) {
		t.Errorf("quería un error de «no existe», dio %v", err)
	}
}

func TestElTextoConSaltosNoRompeElArchivo(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	_ = Append(Entry{Text: "una línea\ny otra"})
	raw, err := os.ReadFile(Path())
	if err != nil {
		t.Fatal(err)
	}
	if strings.Count(strings.TrimSpace(string(raw)), "\n") != 0 {
		t.Errorf("cada dictado es una línea:\n%s", raw)
	}
	entries, _ := Load(0)
	if entries[0].Text != "una línea\ny otra" {
		t.Errorf("volvió mal: %q", entries[0].Text)
	}
}
