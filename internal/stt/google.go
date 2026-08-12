package stt

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/neitanod/dictador/internal/audio"
)

// googleEndpoint es el modo sincrónico de Cloud Speech-to-Text.
const googleEndpoint = "https://speech.googleapis.com/v1/speech:recognize"

// googleMaxSeconds: speech:recognize corta en 1 minuto de audio. Para más largo
// haría falta longRunningRecognize, que devuelve una operación a pollear — otra
// máquina de estados para un caso que el dictado no tiene.
const googleMaxSeconds = 58.0

// Google es Cloud Speech-to-Text: manda el audio a Google y se paga por uso, así
// que es la alternativa, nunca el default.
type Google struct {
	apiKey      string
	language    string
	model       string
	punctuation bool
	timeout     time.Duration
	sampleRate  int
	autoGain    bool
	prompt      string
	ready       bool
	client      *http.Client
	endpoint    string
}

// NewGoogle arma el motor con lo que dice la configuración.
func NewGoogle(opts Options) *Google {
	model := opts.STT.GoogleModel
	if model == "" {
		model = "latest_long"
	}
	rate := opts.SampleRate
	if rate <= 0 {
		rate = 16000
	}
	timeout := seconds(opts.STT.GoogleTimeoutS, 25)
	return &Google{
		apiKey:      GoogleAPIKey(opts.STT),
		language:    GoogleLocale(opts.STT),
		model:       model,
		punctuation: opts.STT.GooglePunctuation,
		timeout:     timeout,
		sampleRate:  rate,
		autoGain:    opts.STT.AutoGain,
		prompt:      opts.STT.InitialPrompt,
		client:      &http.Client{Timeout: timeout},
		endpoint:    googleEndpoint,
	}
}

func (g *Google) Name() string { return "google" }

// SupportsPartial es false: cada parcial sería un viaje a la red y una llamada
// facturada (Google cobra por tramos de 15 s aunque mandes 2), así que en vivo
// no se dibuja nada.
func (g *Google) SupportsPartial() bool { return false }

func (g *Google) Describe() string { return fmt.Sprintf("Google Speech-to-Text (%s)", g.language) }

func (g *Google) Load() error {
	if g.apiKey == "" {
		return errf("Google necesita una API key de Cloud Speech-to-Text. " +
			"Cargala en la configuración o en GOOGLE_API_KEY.")
	}
	g.ready = true
	return nil
}

func (g *Google) Close() {}

func (g *Google) Transcribe(samples []float32, partial bool) (string, error) {
	if partial || len(samples) == 0 {
		return "", nil
	}
	if !g.ready {
		if err := g.Load(); err != nil {
			return "", err
		}
	}
	secs := float64(len(samples)) / float64(g.sampleRate)
	if secs > googleMaxSeconds {
		return "", errf("Google corta el audio en 1 minuto y este dictado dura %.0fs. "+
			"Bajá limits.max_seconds o dictá con Whisper.", secs)
	}
	if g.autoGain {
		samples = NormalizeGain(samples, 0.75, 8.0)
	}

	body := map[string]any{
		"config": map[string]any{
			"encoding":                   "LINEAR16",
			"sampleRateHertz":            g.sampleRate,
			"audioChannelCount":          1,
			"languageCode":               g.language,
			"enableAutomaticPunctuation": g.punctuation,
			"model":                      g.model,
		},
		"audio": map[string]any{
			"content": base64.StdEncoding.EncodeToString(audio.ToPCM16(samples)),
		},
	}
	// Mismo papel que el initial_prompt de Whisper: sesgar nombres propios y
	// jerga para que no los escriba de oído.
	if phrases := splitPhrases(g.prompt); len(phrases) > 0 {
		body["config"].(map[string]any)["speechContexts"] = []any{
			map[string]any{"phrases": phrases},
		}
	}

	data, err := g.post(body)
	if err != nil {
		return "", err
	}
	var chunks []string
	for _, result := range data.Results {
		if len(result.Alternatives) > 0 {
			if t := strings.TrimSpace(result.Alternatives[0].Transcript); t != "" {
				chunks = append(chunks, t)
			}
		}
	}
	return strings.Join(chunks, " "), nil
}

func splitPhrases(prompt string) []string {
	var out []string
	for _, p := range strings.Split(prompt, ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

type googleResponse struct {
	Results []struct {
		Alternatives []struct {
			Transcript string `json:"transcript"`
		} `json:"alternatives"`
	} `json:"results"`
}

func (g *Google) post(body map[string]any) (*googleResponse, error) {
	payload, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}
	endpoint := g.endpoint + "?key=" + url.QueryEscape(g.apiKey)
	req, err := http.NewRequest(http.MethodPost, endpoint, bytes.NewReader(payload))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := g.client.Do(req)
	if err != nil {
		return nil, errf("no pude hablar con Google: %v", err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return nil, errf("Google rechazó la transcripción: %s", googleReason(resp.StatusCode, raw))
	}
	var out googleResponse
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, errf("no entendí la respuesta de Google: %v", err)
	}
	return &out, nil
}

// googleReason saca el mensaje que manda Google, que dice mucho más que el
// código HTTP.
func googleReason(code int, raw []byte) string {
	var payload struct {
		Error struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(raw, &payload); err == nil && payload.Error.Message != "" {
		return fmt.Sprintf("%d %s", code, payload.Error.Message)
	}
	return fmt.Sprintf("%d %s", code, http.StatusText(code))
}
