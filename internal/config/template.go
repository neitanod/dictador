package config

// Template es el config.toml de ejemplo, con los comentarios que explican cada
// valor. Es lo que escribe `dictador config init`, y lo que Save usa de base
// cuando el archivo todavía no existe.
const Template = `# Configuración de dictador — https://github.com/neitanod/dictador
# Se relee con ` + "`dictador run`" + `. Los valores que no pongas toman el default.

[hotkey]
# Lo último es la tecla que dispara, lo de antes tiene que estar apretado.
# Vale un nombre solo ("Control_R"), o una combinación ("AltGr+Control_R").
# Aliases: AltGr, Ctrl, Alt, Shift, Super, RightCtrl, LeftCtrl, Menu…
# ` + "`dictador keys`" + ` lista todo lo que tenés en el teclado.
key = "AltGr+Control_R"
mode = "hold"              # hold = push-to-talk | toggle = apretar/apretar
hold_threshold_ms = 180
cancel_on_other_key = true

[audio]
device = ""                # vacío = fuente default de PipeWire
sample_rate = 16000

[stt]
# faster-whisper transcribe en esta máquina, sin API key y sin mandar tu voz a
# ningún lado; necesita un whisper-server escuchando en whisper_server_url.
# chrome usa la Web Speech API de un Chrome headless: gratis y sin key, pero la
# voz viaja a Google. google usa Cloud Speech-to-Text: se paga por uso.
engine = "faster-whisper"  # faster-whisper | chrome | google
whisper_server_url = "http://127.0.0.1:8080"
chrome_language = ""       # "" = derivado de language (es → es-AR)
google_api_key = ""        # sólo para engine = "google"
google_language = ""       # "" = derivado de language (es → es-AR)

model = "small"            # el que decide el texto que se pega
partial_model = "tiny"     # el que dibuja el texto en vivo mientras hablás
language = "es"            # "" para autodetectar
beam_size = 1              # subilo a 5 si preferís calidad sobre medio segundo
partial_interval_ms = 900
initial_prompt = ""        # jerga o nombres propios que quieras que acierte
vad_filter = true
auto_gain = true           # levanta el volumen si el micrófono viene flojo

[action]
on_release = "paste"       # paste | type | clipboard | keep_open
restore_focus = true
trailing_space = false
strip_final_period = false

[overlay]
enabled = true
# En qué pantalla aparece la ventanita: mouse (donde está el puntero) | focus
# (donde está la ventana que estás usando) | primary | all, o el nombre de una
# salida ("HDMI-1", "eDP-1") para clavarla siempre en la misma.
screen = "mouse"
# Y en qué lugar de esa pantalla: top-left | top-center | top-right | center |
# bottom-left | bottom-center | bottom-right
position = "bottom-center"
font_size = 19
width = 780
hide_delay_ms = 1400

[limits]
max_seconds = 120
min_seconds = 0.35
`
