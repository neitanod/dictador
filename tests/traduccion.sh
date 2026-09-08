#!/usr/bin/env bash
# La letra que elige el idioma, sobre un display virtual.
#
# Lo que se mide acá no es la traducción —eso lo prueban los tests de Go contra
# el traductor de verdad— sino las dos cosas que sólo se ven con un servidor X
# de por medio: que apretar la letra mientras dictás la detecte, y que esa letra
# NO llegue a la ventana de adelante. Lo segundo importa más de lo que parece:
# con el combo apretado, una "e" que se escapa dispara el Ctrl+E de la
# aplicación, que en un navegador abre la barra de búsqueda.
#
# El motor es Chrome, que es el único que traduce. Sin micrófono no va a
# entender nada, y no hace falta: la letra se detecta al soltar, antes de que
# haya texto.
set -uo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
DISPLAY_NUM="${TRANSLATE_DISPLAY:-:98}"
WORK="$(mktemp -d)"
FAILED=0

say() { printf '%s\n' "$*"; }
ok() { printf '  ✓ %s\n' "$*"; }
bad() {
	printf '  ✗ %s\n' "$*"
	FAILED=1
}

need() {
	for binary in "$@"; do
		command -v "$binary" >/dev/null 2>&1 || {
			say "falta $binary: no puedo correr la prueba"
			exit 77
		}
	done
}

cleanup() {
	[[ -n "${DAEMON_PID:-}" ]] && kill "$DAEMON_PID" 2>/dev/null
	[[ -n "${RECEIVER_PID:-}" ]] && kill "$RECEIVER_PID" 2>/dev/null
	[[ -n "${XVFB_PID:-}" ]] && kill "$XVFB_PID" 2>/dev/null
	wait 2>/dev/null
	rm -rf "$WORK"
}
trap cleanup EXIT

need Xvfb xdotool google-chrome

say "la letra del idioma en $DISPLAY_NUM"

( cd "$ROOT" && go build -o "$WORK/dictador" . ) || { say "no compiló"; exit 1; }
( cd "$ROOT" && go build -o "$WORK/receiver" ./tests/receiver ) ||
	{ say "no compiló el receiver"; exit 1; }

Xvfb "$DISPLAY_NUM" -screen 0 1280x800x24 >/dev/null 2>&1 &
XVFB_PID=$!
sleep 1
DISPLAY="$DISPLAY_NUM" xdotool getdisplaygeometry >/dev/null 2>&1 ||
	{ say "Xvfb no levantó"; exit 1; }
ok "display virtual arriba"

export XDG_CONFIG_HOME="$WORK/config"
export XDG_STATE_HOME="$WORK/state"
export XDG_RUNTIME_DIR="$WORK/run"
mkdir -p "$XDG_RUNTIME_DIR" "$XDG_CONFIG_HOME/dictador"

# La tecla del dictado va sin modificadores para poder sintetizarla, y la letra
# del idioma es la "e", como de fábrica.
escribir_config() {
	cat > "$XDG_CONFIG_HOME/dictador/config.toml" <<EOF
[hotkey]
key = "F9"
mode = "hold"
hold_threshold_ms = 120
cancel_on_other_key = true

[stt]
engine = "chrome"
chrome_headless = true
partial_interval_ms = 0

[action]
on_release = "paste"

[overlay]
enabled = false

[limits]
min_seconds = 0.2

[translate]
enabled = $1
preview_ms = 0

[translate.keys]
e = "en"
EOF
}

# dictar mantiene la tecla del dictado, aprieta la letra en el medio, y suelta
# las dos casi juntas, que es lo que hace la mano.
dictar() {
	DISPLAY="$DISPLAY_NUM" xdotool keydown F9
	sleep 0.6
	DISPLAY="$DISPLAY_NUM" xdotool keydown e
	sleep 0.6
	DISPLAY="$DISPLAY_NUM" xdotool keyup e
	DISPLAY="$DISPLAY_NUM" xdotool keyup F9
	sleep 3
}

# ---- con la traducción prendida ------------------------------------------

escribir_config true
DISPLAY="$DISPLAY_NUM" "$WORK/receiver" --editor "$WORK/escrito.txt" dictador-receiver \
	> "$WORK/receiver.log" 2>&1 &
RECEIVER_PID=$!
sleep 1

DISPLAY="$DISPLAY_NUM" "$WORK/dictador" run -v > "$WORK/daemon.log" 2>&1 &
DAEMON_PID=$!
sleep 3
kill -0 "$DAEMON_PID" 2>/dev/null || {
	say "el daemon no arrancó:"
	sed 's/^/    /' "$WORK/daemon.log"
	exit 1
}

dictar

grep -q "grabando" "$WORK/daemon.log" && ok "detectó el press y empezó a grabar" ||
	bad "no detectó el press (¿XInput2 raw?)"
grep -q "va traducido a en" "$WORK/daemon.log" &&
	ok "la letra apretada al soltar eligió el inglés" ||
	bad "no detectó la letra del idioma"

ESCRITO="$(cat "$WORK/escrito.txt" 2>/dev/null)"
if [[ "$ESCRITO" == *e* ]]; then
	bad "la letra se escapó a la ventana de adelante: «$ESCRITO»"
else
	ok "la letra no llegó a la ventana de adelante"
fi

kill "$DAEMON_PID" 2>/dev/null
wait "$DAEMON_PID" 2>/dev/null
kill "$RECEIVER_PID" 2>/dev/null
wait "$RECEIVER_PID" 2>/dev/null

# ---- y con la traducción apagada -----------------------------------------
#
# Esta mitad es la que hace que la otra signifique algo: sin la traducción en
# juego, la misma "e" tiene que llegar a la ventana como cualquier tecla. Si no
# llegara, el test de arriba estaría pasando por cualquier otro motivo.
say ""
say "y con la traducción apagada"

escribir_config false
rm -f "$WORK/escrito.txt"
DISPLAY="$DISPLAY_NUM" "$WORK/receiver" --editor "$WORK/escrito.txt" dictador-receiver \
	> "$WORK/receiver2.log" 2>&1 &
RECEIVER_PID=$!
sleep 1
DISPLAY="$DISPLAY_NUM" "$WORK/dictador" run -v > "$WORK/daemon2.log" 2>&1 &
DAEMON_PID=$!
sleep 3

dictar

ESCRITO="$(cat "$WORK/escrito.txt" 2>/dev/null)"
if [[ "$ESCRITO" == *e* ]]; then
	ok "la tecla vuelve a ser una tecla: «$ESCRITO»"
else
	bad "la letra tampoco llegó con la traducción apagada: «$ESCRITO»"
fi
grep -q "va traducido a" "$WORK/daemon2.log" &&
	bad "tradujo con la traducción apagada" ||
	ok "con la traducción apagada no se traduce nada"

say ""
if [[ "$FAILED" == "0" ]]; then
	say "todo bien"
else
	say "hubo fallas; los logs quedaron en $WORK"
	trap - EXIT
	[[ -n "${DAEMON_PID:-}" ]] && kill "$DAEMON_PID" 2>/dev/null
	[[ -n "${RECEIVER_PID:-}" ]] && kill "$RECEIVER_PID" 2>/dev/null
	[[ -n "${XVFB_PID:-}" ]] && kill "$XVFB_PID" 2>/dev/null
fi
exit "$FAILED"
