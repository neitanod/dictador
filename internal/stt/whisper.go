package stt

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/neitanod/dictador/internal/audio"
)

// Whisper local, por HTTP contra el whisper-server de whisper.cpp.
//
// El original en Python tenía el modelo adentro del proceso (faster-whisper) y
// le pedía una transcripción cada 900 ms para dibujar el texto en vivo. Invocar
// un `whisper-cli` por dictado perdería eso: cada parcial sería cargar el modelo
// de nuevo. whisper-server mantiene el modelo residente y contesta por HTTP
// local, así que los parciales siguen siendo baratos y el binario sigue sin
// necesitar CGO.
//
//	whisper-server -m models/ggml-small.bin --host 127.0.0.1 --port 8080

// Whisper habla con un whisper-server que ya esté escuchando.
type Whisper struct {
	baseURL  string
	model    string
	language string
	prompt   string
	beamSize int
	autoGain bool
	rate     int
	client   *http.Client
	ready    bool
}

// NewWhisper arma el motor con lo que dice la configuración.
func NewWhisper(opts Options) *Whisper {
	rate := opts.SampleRate
	if rate <= 0 {
		rate = 16000
	}
	base := strings.TrimRight(strings.TrimSpace(opts.STT.WhisperServerURL), "/")
	if base == "" {
		base = "http://127.0.0.1:8080"
	}
	return &Whisper{
		baseURL:  base,
		model:    opts.STT.Model,
		language: opts.STT.Language,
		prompt:   opts.STT.InitialPrompt,
		beamSize: opts.STT.BeamSize,
		autoGain: opts.STT.AutoGain,
		rate:     rate,
		client:   &http.Client{Timeout: 60 * time.Second},
	}
}

func (w *Whisper) Name() string          { return "whisper" }
func (w *Whisper) SupportsPartial() bool { return true }

func (w *Whisper) Describe() string {
	return fmt.Sprintf("Whisper local por whisper-server (%s)", w.baseURL)
}

// WhisperAvailable dice si hay un whisper-server contestando.
func WhisperAvailable(opts Options) bool {
	return NewWhisper(opts).Load() == nil
}

// Load confirma que del otro lado haya un whisper-server antes del primer
// dictado.
//
// No alcanza con que algo conteste en ese puerto: en una máquina de desarrollo
// el 8080 lo ocupa cualquier cosa, y darlo por bueno significa anunciar que
// Whisper está disponible para fallar recién cuando alguien dicta. La página
// que sirve whisper.cpp se nombra, así que se la busca.
func (w *Whisper) Load() error {
	client := &http.Client{Timeout: 2 * time.Second}
	resp, err := client.Get(w.baseURL + "/")
	if err != nil {
		return errf("no hay nada escuchando en %s. Levantá el whisper-server con "+
			"`whisper-server -m <modelo>.bin --port 8080`, o elegí el motor chrome.", w.baseURL)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 16<<10))
	haystack := strings.ToLower(string(body) + " " + resp.Header.Get("Server"))
	if !strings.Contains(haystack, "whisper") {
		return errf("en %s hay algo contestando, pero no parece un whisper-server. "+
			"Fijate qué está ocupando ese puerto o cambiá stt.whisper_server_url.", w.baseURL)
	}
	w.ready = true
	return nil
}

func (w *Whisper) Close() {}

// Transcribe manda el audio como WAV y devuelve el texto.
//
// Los parciales van con beam 1 y sin prompt: importa que salgan rápido, la
// calidad la arregla la pasada final.
func (w *Whisper) Transcribe(samples []float32, partial bool) (string, error) {
	if len(samples) == 0 {
		return "", nil
	}
	if !w.ready {
		if err := w.Load(); err != nil {
			return "", err
		}
	}
	if w.autoGain {
		samples = NormalizeGain(samples, 0.75, 8.0)
	}
	wav := audio.WAV(audio.ToPCM16(samples), w.rate)

	var body bytes.Buffer
	form := multipart.NewWriter(&body)
	part, err := form.CreateFormFile("file", "dictado.wav")
	if err != nil {
		return "", err
	}
	if _, err := part.Write(wav); err != nil {
		return "", err
	}
	fields := map[string]string{
		"response_format": "json",
		"temperature":     "0",
	}
	if w.language != "" {
		fields["language"] = w.language
	}
	if partial {
		fields["beam_size"] = "1"
	} else {
		if w.beamSize > 0 {
			fields["beam_size"] = strconv.Itoa(w.beamSize)
		}
		if w.prompt != "" {
			fields["prompt"] = w.prompt
		}
	}
	for key, value := range fields {
		if err := form.WriteField(key, value); err != nil {
			return "", err
		}
	}
	if err := form.Close(); err != nil {
		return "", err
	}

	req, err := http.NewRequest(http.MethodPost, w.baseURL+"/inference", &body)
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", form.FormDataContentType())
	resp, err := w.client.Do(req)
	if err != nil {
		return "", errf("whisper-server no contestó: %v", err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return "", errf("whisper-server devolvió %d: %s", resp.StatusCode,
			strings.TrimSpace(string(raw)))
	}
	var out struct {
		Text  string `json:"text"`
		Error string `json:"error"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		// Algunos builds contestan el texto pelado.
		return strings.TrimSpace(string(raw)), nil
	}
	if out.Error != "" {
		return "", errf("whisper-server: %s", out.Error)
	}
	return strings.TrimSpace(out.Text), nil
}
