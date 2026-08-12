package stt

import (
	"encoding/json"
	"math"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/neitanod/dictador/internal/config"
)

func TestCanonicalAceptaLosAlias(t *testing.T) {
	for spec, want := range map[string]string{
		"faster-whisper": "whisper",
		"WHISPER":        "whisper",
		" local ":        "whisper",
		"web-speech":     "chrome",
		"gcloud":         "google",
	} {
		got, err := Canonical(spec)
		if err != nil {
			t.Errorf("%q: %v", spec, err)
			continue
		}
		if got != want {
			t.Errorf("%q → %q, quería %q", spec, got, want)
		}
	}
	if _, err := Canonical("vosk"); err == nil {
		t.Error("un motor que no existe tendría que fallar")
	}
}

func TestGoogleLocaleDerivaDelIdiomaCorto(t *testing.T) {
	cases := []struct {
		cfg  config.STT
		want string
	}{
		{config.STT{Language: "es"}, "es-AR"},
		{config.STT{Language: "en"}, "en-US"},
		{config.STT{Language: ""}, "es-AR"},
		{config.STT{Language: "es", GoogleLanguage: "es-ES"}, "es-ES"},
		{config.STT{Language: "ja-JP"}, "ja-JP"},
		{config.STT{Language: "ja"}, "es-AR"}, // sin mapa, cae al default
	}
	for _, c := range cases {
		if got := GoogleLocale(c.cfg); got != c.want {
			t.Errorf("%+v → %q, quería %q", c.cfg, got, c.want)
		}
	}
}

func TestGoogleAPIKeyPrefiereElEntorno(t *testing.T) {
	t.Setenv("GOOGLE_API_KEY", "")
	t.Setenv("DICTADO_GOOGLE_API_KEY", "")
	t.Setenv("DICTADOR_GOOGLE_API_KEY", "")
	if got := GoogleAPIKey(config.STT{GoogleAPIKey: "del-archivo"}); got != "del-archivo" {
		t.Errorf("sin entorno tendría que usar el archivo, usó %q", got)
	}
	t.Setenv("GOOGLE_API_KEY", "del-entorno")
	if got := GoogleAPIKey(config.STT{GoogleAPIKey: "del-archivo"}); got != "del-entorno" {
		t.Errorf("el entorno tiene que ganar, ganó %q", got)
	}
}

func TestNormalizeGainLevantaLoFlojoYNoTocaLoQueYaSuena(t *testing.T) {
	flojo := []float32{0.05, -0.05, 0.02}
	subido := NormalizeGain(flojo, 0.75, 8.0)
	if math.Abs(float64(subido[0])-0.4) > 0.001 { // techo de ganancia: 8×
		t.Errorf("ganancia mal aplicada: %v", subido)
	}
	fuerte := []float32{0.9, -0.8}
	if got := NormalizeGain(fuerte, 0.75, 8.0); got[0] != 0.9 {
		t.Errorf("no debería tocar una toma que ya suena: %v", got)
	}
	silencio := []float32{0.00001, 0}
	if got := NormalizeGain(silencio, 0.75, 8.0); got[0] != 0.00001 {
		t.Errorf("no debería amplificar el silencio: %v", got)
	}
	if got := NormalizeGain(nil, 0.75, 8.0); got != nil {
		t.Errorf("con audio vacío tendría que devolver lo mismo")
	}
}

func TestGoogleArmaElPedidoYLeeLaRespuesta(t *testing.T) {
	var body map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&body)
		if r.URL.Query().Get("key") != "AIza-de-prueba" {
			t.Errorf("no mandó la key: %q", r.URL.RawQuery)
		}
		_, _ = w.Write([]byte(`{"results":[{"alternatives":[{"transcript":"hola "}]},
		                                   {"alternatives":[{"transcript":" mundo"}]}]}`))
	}))
	defer server.Close()

	g := NewGoogle(Options{
		STT:        config.STT{GoogleAPIKey: "AIza-de-prueba", Language: "es", InitialPrompt: "Robotín, Sebastián"},
		SampleRate: 16000,
	})
	g.endpoint = server.URL
	t.Setenv("GOOGLE_API_KEY", "")
	g.apiKey = "AIza-de-prueba"

	text, err := g.Transcribe(make([]float32, 16000), false)
	if err != nil {
		t.Fatal(err)
	}
	if text != "hola mundo" {
		t.Errorf("texto = %q", text)
	}
	cfg := body["config"].(map[string]any)
	if cfg["languageCode"] != "es-AR" {
		t.Errorf("languageCode = %v", cfg["languageCode"])
	}
	if cfg["sampleRateHertz"].(float64) != 16000 {
		t.Errorf("sampleRateHertz = %v", cfg["sampleRateHertz"])
	}
	contexts, ok := cfg["speechContexts"].([]any)
	if !ok || len(contexts) != 1 {
		t.Fatalf("faltan los speechContexts del initial_prompt: %v", cfg["speechContexts"])
	}
	phrases := contexts[0].(map[string]any)["phrases"].([]any)
	if len(phrases) != 2 || phrases[0] != "Robotín" {
		t.Errorf("phrases = %v", phrases)
	}
}

func TestGoogleExplicaElErrorQueMandaGoogle(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":{"message":"API key not valid"}}`))
	}))
	defer server.Close()

	g := NewGoogle(Options{STT: config.STT{GoogleAPIKey: "mala"}, SampleRate: 16000})
	g.endpoint = server.URL
	g.apiKey = "mala"
	_, err := g.Transcribe(make([]float32, 16000), false)
	if err == nil || !strings.Contains(err.Error(), "API key not valid") {
		t.Errorf("el mensaje de Google tendría que llegar entero: %v", err)
	}
}

func TestGoogleNoTranscribeParciales(t *testing.T) {
	g := NewGoogle(Options{STT: config.STT{GoogleAPIKey: "x"}, SampleRate: 16000})
	if g.SupportsPartial() {
		t.Error("Google cobra cada llamada: no tiene que soportar parciales")
	}
	if text, err := g.Transcribe(make([]float32, 16000), true); text != "" || err != nil {
		t.Errorf("un parcial tiene que salir vacío y sin error: %q %v", text, err)
	}
}

func TestGoogleSeNiegaConAudioMasLargoQueElLimite(t *testing.T) {
	g := NewGoogle(Options{STT: config.STT{GoogleAPIKey: "x"}, SampleRate: 16000})
	g.ready = true
	_, err := g.Transcribe(make([]float32, 16000*90), false)
	if err == nil || !strings.Contains(err.Error(), "1 minuto") {
		t.Errorf("tendría que avisar del límite de Google: %v", err)
	}
}

func TestWhisperMandaElWavYLeeElTexto(t *testing.T) {
	var gotLanguage, gotBeam string
	var gotWAV []byte
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/" {
			w.WriteHeader(http.StatusOK)
			return
		}
		if err := r.ParseMultipartForm(1 << 20); err != nil {
			t.Errorf("no era multipart: %v", err)
		}
		gotLanguage = r.FormValue("language")
		gotBeam = r.FormValue("beam_size")
		file, _, err := r.FormFile("file")
		if err == nil {
			gotWAV = make([]byte, 12)
			_, _ = file.Read(gotWAV)
		}
		_, _ = w.Write([]byte(`{"text":"  hola mundo  "}`))
	}))
	defer server.Close()

	engine := NewWhisper(Options{
		STT:        config.STT{WhisperServerURL: server.URL, Language: "es", BeamSize: 5},
		SampleRate: 16000,
	})
	text, err := engine.Transcribe(make([]float32, 1600), false)
	if err != nil {
		t.Fatal(err)
	}
	if text != "hola mundo" {
		t.Errorf("texto = %q", text)
	}
	if gotLanguage != "es" || gotBeam != "5" {
		t.Errorf("campos = %q %q", gotLanguage, gotBeam)
	}
	if string(gotWAV[:4]) != "RIFF" {
		t.Errorf("no mandó un WAV: %q", gotWAV)
	}
}

func TestWhisperExplicaQueNoHayServer(t *testing.T) {
	engine := NewWhisper(Options{STT: config.STT{WhisperServerURL: "http://127.0.0.1:1"}})
	err := engine.Load()
	if err == nil || !strings.Contains(err.Error(), "whisper-server") {
		t.Errorf("tendría que decir cómo levantarlo: %v", err)
	}
}

// ---- Chrome: el protocolo completo, con la página simulada ----------------

// fakeChrome deja un ejecutable que se queda quieto: alcanza para que el motor
// crea que el browser arrancó, y así el test puede hacer de página.
func fakeChrome(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "google-chrome")
	script := "#!/bin/sh\nsleep 30\n"
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}

// pageStub imita lo que hace el JavaScript de la página: pregunta por comandos
// con long polling y devuelve eventos.
type pageStub struct {
	t    *testing.T
	base string
	stop chan struct{}
}

func (p *pageStub) event(kind, text string) {
	body := strings.NewReader(`{"kind":"` + kind + `","text":"` + text + `"}`)
	resp, err := http.Post(p.base+"/event", "application/json", body)
	if err != nil {
		return // el test terminó y el server cerró: no es noticia
	}
	_ = resp.Body.Close()
}

// keepSayingReady imita a la página real, que saluda apenas carga: como el
// motor rehace el canal de "listo" cada vez que lanza el browser, el saludo se
// repite hasta que el test termina.
func (p *pageStub) keepSayingReady() {
	go func() {
		for {
			select {
			case <-p.stop:
				return
			default:
			}
			p.event("ready", "")
			time.Sleep(30 * time.Millisecond)
		}
	}()
}

func (p *pageStub) loop(onCommand func(string)) {
	client := &http.Client{Timeout: 25 * time.Second}
	for {
		select {
		case <-p.stop:
			return
		default:
		}
		resp, err := client.Get(p.base + "/command")
		if err != nil {
			return
		}
		buf := make([]byte, 16)
		n, _ := resp.Body.Read(buf)
		_ = resp.Body.Close()
		if command := strings.TrimSpace(string(buf[:n])); command != "" && command != "noop" {
			onCommand(command)
		}
	}
}

func startChromeStub(t *testing.T) (*Chrome, *pageStub) {
	t.Helper()
	engine := NewChrome(Options{
		STT: config.STT{
			ChromeBinary:        fakeChrome(t),
			Language:            "es",
			ChromeReadyTimeoutS: 5,
			ChromeFinalTimeoutS: 2,
		},
	})
	t.Cleanup(engine.Close)

	// El server tiene que estar antes de que la página pueda hablarle.
	engine.mu.Lock()
	if err := engine.startServer(); err != nil {
		engine.mu.Unlock()
		t.Fatal(err)
	}
	engine.mu.Unlock()

	page := &pageStub{
		t:    t,
		base: "http://" + engine.listener.Addr().String(),
		stop: make(chan struct{}),
	}
	t.Cleanup(func() { close(page.stop) })
	return engine, page
}

func TestChromeEsperaElSaludoDeLaPagina(t *testing.T) {
	engine, page := startChromeStub(t)
	go func() {
		time.Sleep(50 * time.Millisecond)
		page.event("ready", "")
	}()
	if err := engine.Load(); err != nil {
		t.Fatalf("no arrancó: %v", err)
	}
	if !strings.Contains(engine.Describe(), "es-AR") {
		t.Errorf("Describe = %q", engine.Describe())
	}
	if !strings.Contains(engine.page(), `const LANG = "es-AR"`) {
		t.Error("la página tendría que llevar el idioma adentro")
	}
}

func TestChromeDaParcialesYElFinal(t *testing.T) {
	engine, page := startChromeStub(t)
	page.keepSayingReady()
	go page.loop(func(command string) {
		switch command {
		case "start":
			page.event("partial", "hola")
			page.event("partial", "hola mundo")
		case "stop":
			page.event("final", "hola mundo")
		}
	})

	if err := engine.StartLive(); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(2 * time.Second)
	for engine.PartialText() == "" && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if got := engine.PartialText(); got == "" {
		t.Fatal("nunca llegó un parcial")
	}
	text, err := engine.FinishLive()
	if err != nil {
		t.Fatal(err)
	}
	if text != "hola mundo" {
		t.Errorf("final = %q", text)
	}
}

func TestChromeSeQuedaConElParcialSiElFinalNoLlega(t *testing.T) {
	engine, page := startChromeStub(t)
	page.keepSayingReady()
	go page.loop(func(command string) {
		if command == "start" {
			page.event("partial", "quedó a medias")
		}
		// El "stop" se ignora a propósito: es el caso del final que no vuelve.
	})

	if err := engine.StartLive(); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(2 * time.Second)
	for engine.PartialText() == "" && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	text, err := engine.FinishLive()
	if err != nil {
		t.Fatal(err)
	}
	if text != "quedó a medias" {
		t.Errorf("el último parcial es mejor que nada: %q", text)
	}
}

func TestChromeTratraNoSpeechComoDictadoVacio(t *testing.T) {
	engine, page := startChromeStub(t)
	page.keepSayingReady()
	go page.loop(func(command string) {
		if command == "stop" {
			page.event("error", "no-speech")
		}
	})

	if err := engine.StartLive(); err != nil {
		t.Fatal(err)
	}
	text, err := engine.FinishLive()
	if err != nil {
		t.Errorf("no-speech no es un error: %v", err)
	}
	if text != "" {
		t.Errorf("tendría que salir vacío, salió %q", text)
	}
}

func TestChromeTraduceLosErroresDeLaWebSpeech(t *testing.T) {
	engine, page := startChromeStub(t)
	page.keepSayingReady()
	go page.loop(func(command string) {
		if command == "start" {
			page.event("error", "network")
		}
	})

	if err := engine.StartLive(); err != nil {
		t.Fatal(err)
	}
	_, err := engine.FinishLive()
	if err == nil || !strings.Contains(err.Error(), "internet") {
		t.Errorf("tendría que explicar el error de red: %v", err)
	}
}

func TestChromeCancelarTiraLoQueEscucho(t *testing.T) {
	engine, page := startChromeStub(t)
	page.keepSayingReady()
	go page.loop(func(command string) {
		if command == "start" {
			page.event("partial", "algo que no quiero")
		}
	})

	if err := engine.StartLive(); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(2 * time.Second)
	for engine.PartialText() == "" && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	engine.AbortLive()
	if got := engine.PartialText(); got != "" {
		t.Errorf("después de cancelar no puede quedar texto: %q", got)
	}
	if text, err := engine.FinishLive(); text != "" || err != nil {
		t.Errorf("terminar sin estar escuchando no devuelve nada: %q %v", text, err)
	}
}

func TestChromeEsUnMotorVivo(t *testing.T) {
	var engine Engine = NewChrome(Options{})
	if _, ok := engine.(LiveEngine); !ok {
		t.Error("Chrome tiene que implementar LiveEngine")
	}
	var whisper Engine = NewWhisper(Options{})
	if _, ok := whisper.(LiveEngine); ok {
		t.Error("Whisper recibe el audio grabado: no es un motor vivo")
	}
}
