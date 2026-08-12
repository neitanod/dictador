package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeTemp(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func read(t *testing.T, path string) string {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

func TestLoadAppliesDefaultsToWhatIsMissing(t *testing.T) {
	path := writeTemp(t, "[stt]\nengine = \"chrome\"\n")
	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.STT.Engine != "chrome" {
		t.Errorf("engine = %q, quería chrome", cfg.STT.Engine)
	}
	if cfg.Hotkey.Key != "AltGr+Control_R" {
		t.Errorf("la tecla debería venir del default, vino %q", cfg.Hotkey.Key)
	}
	if cfg.Audio.SampleRate != 16000 {
		t.Errorf("sample_rate = %d, quería 16000", cfg.Audio.SampleRate)
	}
	if cfg.Path != path {
		t.Errorf("Path = %q, quería %q", cfg.Path, path)
	}
}

func TestLoadSinArchivoDaLosDefaults(t *testing.T) {
	// Con XDG apuntando a un directorio vacío no hay archivo en ningún lado.
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	cfg, err := Load("")
	if err != nil {
		t.Fatalf("que no haya config no es un error: %v", err)
	}
	if cfg.Path != "" {
		t.Errorf("Path debería quedar vacío, quedó %q", cfg.Path)
	}
	if cfg.Limits.MinSeconds != 0.35 {
		t.Errorf("min_seconds = %v", cfg.Limits.MinSeconds)
	}
}

func TestLoadCaeAlConfigDelDictadoViejo(t *testing.T) {
	base := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", base)
	legacy := filepath.Join(base, "dictado")
	if err := os.MkdirAll(legacy, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(legacy, "config.toml"),
		[]byte("[hotkey]\nkey = \"Menu\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load("")
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Hotkey.Key != "Menu" {
		t.Errorf("no leyó el config del dictado viejo: key = %q", cfg.Hotkey.Key)
	}
}

func TestLoadPrefiereElConfigPropioSobreElViejo(t *testing.T) {
	base := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", base)
	for dir, key := range map[string]string{"dictado": "Menu", "dictador": "Pause"} {
		full := filepath.Join(base, dir)
		if err := os.MkdirAll(full, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(full, "config.toml"),
			[]byte("[hotkey]\nkey = \""+key+"\"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	cfg, _ := Load("")
	if cfg.Hotkey.Key != "Pause" {
		t.Errorf("debería mandar el config de dictador: key = %q", cfg.Hotkey.Key)
	}
}

func TestSaveConservaLosComentarios(t *testing.T) {
	path := writeTemp(t, `# Configuración de dictador
[stt]
# faster-whisper es local; chrome manda la voz a Google
engine = "faster-whisper"  # el que transcribe
language = "es"            # "" para autodetectar
`)
	if _, err := Save(path, []Setting{{Section: "stt", Key: "engine", Value: "chrome"}}); err != nil {
		t.Fatal(err)
	}
	out := read(t, path)
	if !strings.Contains(out, `engine = "chrome"  # el que transcribe`) {
		t.Errorf("no reemplazó conservando el comentario al margen:\n%s", out)
	}
	if !strings.Contains(out, "# faster-whisper es local") {
		t.Errorf("se comió el comentario de arriba:\n%s", out)
	}
	if !strings.Contains(out, `language = "es"            # "" para autodetectar`) {
		t.Errorf("tocó una línea que no era:\n%s", out)
	}
}

func TestSaveAgregaLaClaveQueFaltaAlFinalDeSuSeccion(t *testing.T) {
	path := writeTemp(t, `[stt]
engine = "chrome"

[action]
on_release = "paste"
`)
	if _, err := Save(path, []Setting{{Section: "stt", Key: "google_api_key", Value: "AIza-secreta"}}); err != nil {
		t.Fatal(err)
	}
	out := read(t, path)
	lines := strings.Split(strings.TrimSpace(out), "\n")
	if lines[2] != `google_api_key = "AIza-secreta"` {
		t.Errorf("la clave nueva tendría que ir al final de [stt]:\n%s", out)
	}
	if !strings.Contains(out, "[action]\non_release") {
		t.Errorf("rompió la sección de abajo:\n%s", out)
	}
}

func TestSaveCreaLaSeccionQueNoEstaba(t *testing.T) {
	path := writeTemp(t, "[stt]\nengine = \"chrome\"\n")
	if _, err := Save(path, []Setting{{Section: "limits", Key: "max_seconds", Value: 90}}); err != nil {
		t.Fatal(err)
	}
	out := read(t, path)
	if !strings.Contains(out, "[limits]\nmax_seconds = 90") {
		t.Errorf("no creó la sección:\n%s", out)
	}
}

func TestSaveNoConfundeClavesDeOtraSeccion(t *testing.T) {
	// `device` está en [audio] y en [stt]: tocar una no puede tocar la otra.
	path := writeTemp(t, `[audio]
device = "mic-viejo"

[stt]
device = "cpu"
`)
	if _, err := Save(path, []Setting{{Section: "stt", Key: "device", Value: "cuda"}}); err != nil {
		t.Fatal(err)
	}
	out := read(t, path)
	if !strings.Contains(out, `device = "mic-viejo"`) {
		t.Errorf("pisó el device de [audio]:\n%s", out)
	}
	if !strings.Contains(out, `device = "cuda"`) {
		t.Errorf("no escribió el device de [stt]:\n%s", out)
	}
}

func TestSaveEscribeCadaTipoComoTOML(t *testing.T) {
	path := writeTemp(t, "[stt]\nengine = \"chrome\"\n")
	_, err := Save(path, []Setting{
		{Section: "stt", Key: "vad_filter", Value: false},
		{Section: "stt", Key: "beam_size", Value: 5},
		{Section: "limits", Key: "min_seconds", Value: 0.35},
		{Section: "stt", Key: "initial_prompt", Value: `dijo "hola"`},
	})
	if err != nil {
		t.Fatal(err)
	}
	out := read(t, path)
	for _, want := range []string{
		"vad_filter = false",
		"beam_size = 5",
		"min_seconds = 0.35",
		`initial_prompt = "dijo \"hola\""`,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("falta %q en:\n%s", want, out)
		}
	}
	// Y lo guardado tiene que volver a leerse.
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("el archivo guardado no vuelve a parsear: %v", err)
	}
	if cfg.STT.BeamSize != 5 || cfg.STT.VadFilter || cfg.STT.InitialPrompt != `dijo "hola"` {
		t.Errorf("releído mal: %+v", cfg.STT)
	}
}

func TestSaveCreaElArchivoConLaPlantilla(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nuevo", "config.toml")
	if _, err := Save(path, []Setting{{Section: "stt", Key: "engine", Value: "google"}}); err != nil {
		t.Fatal(err)
	}
	out := read(t, path)
	if !strings.Contains(out, "[hotkey]") || !strings.Contains(out, `engine = "google"`) {
		t.Errorf("debería salir la plantilla con el valor aplicado:\n%s", out)
	}
}

func TestLaPlantillaParseaYCoincideConLosDefaults(t *testing.T) {
	path := writeTemp(t, Template)
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("la plantilla no parsea: %v", err)
	}
	def := Defaults()
	if cfg.Hotkey != def.Hotkey {
		t.Errorf("[hotkey] de la plantilla ≠ defaults:\n%+v\n%+v", cfg.Hotkey, def.Hotkey)
	}
	if cfg.Action != def.Action {
		t.Errorf("[action] de la plantilla ≠ defaults:\n%+v\n%+v", cfg.Action, def.Action)
	}
	if cfg.Limits != def.Limits {
		t.Errorf("[limits] de la plantilla ≠ defaults:\n%+v\n%+v", cfg.Limits, def.Limits)
	}
	if cfg.STT != def.STT {
		t.Errorf("[stt] de la plantilla ≠ defaults:\n%+v\n%+v", cfg.STT, def.STT)
	}
}
