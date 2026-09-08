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
trailing_space = false     # un espacio atrás del texto, para dictar de a bloques
strip_final_period = false
# Las terminales pegan con Ctrl+Shift+V y el resto con Ctrl+V. Si tenés una que
# la lista de fábrica no conoce, sumá acá su clase de ventana: la dice
# "dictador window", parado en esa ventana.
terminal_classes = []

[overlay]
enabled = true
# En qué pantalla aparece la ventanita: mouse (donde está el puntero) | focus
# (donde está la ventana que estás usando) | primary | all, o el nombre de una
# salida ("HDMI-1", "eDP-1") para clavarla siempre en la misma.
screen = "mouse"
# Y en qué lugar de esa pantalla: top-left | top-center | top-right | center |
# bottom-left | bottom-center | bottom-right
position = "bottom-center"
font_size = 19             # en puntos, rasterizados al DPI de tu pantalla
width = 780
hide_delay_ms = 1400

[limits]
max_seconds = 120
min_seconds = 0.35

[translate]
# Traducción instantánea: mientras dictás, tocá la letra del idioma y lo que
# dijiste se pega traducido. Otro toque la apaga, y otra letra manda sobre la
# anterior. Sin tocar ninguna, el texto se pega tal cual.
# Anda sólo con engine = "chrome", que es el que puede traducir gratis desde el
# mismo navegador que te escucha.
enabled = true
# De dónde sale la traducción:
#   web = la página de translate.google.com, la que traduce por sentido
#         ("mañana voy a estar hecho mierda" → "tomorrow I'm going to be a
#         wreck"). Tarda cerca de un segundo.
#   api = el endpoint público, que traduce palabra por palabra ("...going to be
#         shit") y contesta en trescientos milisegundos.
# Con web, el endpoint queda igual de red: si la página no contesta, el dictado
# se pega igual, traducido por el otro camino y con el aviso al lado.
mode = "web"               # web | api
# Con sticky = true el idioma queda puesto para el dictado siguiente: tocás la
# letra una vez y la conversación entera sale traducida, hasta que la toques de
# nuevo. En false, cada dictado arranca sin idioma.
sticky = false
preview_ms = 1200          # el ratito que se muestra antes de pegar (Esc cancela)
timeout_s = 20             # si el traductor tarda más, se pega lo que dijiste

# La letra que tocás y el idioma al que va. Códigos de Google Translate:
# en, pt, fr, it, de, ja, zh-CN…
[translate.keys]
e = "en"
p = "pt"

[commands]
# Comandos hablados: decir "coma" escribe una coma, "abre pregunta" escribe ¿,
# "entre corchetes" escribe [] y deja el cursor en el medio, "enter" y "tab" son
# la tecla de verdad, y "borrar palabra" / "borrá eso" deshacen lo que dictaste.
# ` + "`dictador commands`" + ` lista todos los que hay.
enabled = true

# Los tuyos. La clave es lo que decís (sin importar tildes ni mayúsculas) y el
# valor lo que se escribe. Un valor vacío apaga un comando de fábrica.
# El espaciado se deduce solo: un valor que empieza con un signo de cierre se
# pega a la palabra anterior, y uno que termina en uno de apertura a la que
# sigue. Si querés mandar los espacios vos, ponelos en el valor.
[commands.replacements]
# "dos puntos" = ":"
# "flecha" = "→"
# "arroba" = "@"
`
