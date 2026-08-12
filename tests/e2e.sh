#!/usr/bin/env bash
# End-to-end sobre un display virtual: tecla → micrófono → motor → portapapeles.
#
# Es la única forma de saber que el hotkey XInput2, la máquina de estados y la
# propiedad de la selección siguen funcionando juntos. El motor de voz lo hace
# un server de mentira que contesta siempre lo mismo: acá se mide el camino, no
# la calidad del reconocimiento, que se mide con voz de verdad y aparte.
set -uo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
DISPLAY_NUM="${E2E_DISPLAY:-:99}"
PORT="${E2E_PORT:-8099}"
EXPECTED="hola mundo del dictador"
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
			say "falta $binary: no puedo correr el e2e"
			exit 77
		}
	done
}

cleanup() {
	[[ -n "${DAEMON_PID:-}" ]] && kill "$DAEMON_PID" 2>/dev/null
	[[ -n "${RECEIVER_PID:-}" ]] && kill "$RECEIVER_PID" 2>/dev/null
	[[ -n "${STUB_PID:-}" ]] && kill "$STUB_PID" 2>/dev/null
	[[ -n "${XVFB_PID:-}" ]] && kill "$XVFB_PID" 2>/dev/null
	wait 2>/dev/null
	rm -rf "$WORK"
}
trap cleanup EXIT

need Xvfb xdotool xclip python3

say "e2e de dictador en $DISPLAY_NUM"

# El binario, recién compilado, para que el test no mida una versión vieja.
( cd "$ROOT" && go build -o "$WORK/dictador" . ) || { say "no compiló"; exit 1; }
( cd "$ROOT" && go build -o "$WORK/receiver" ./tests/receiver ) ||
	{ say "no compiló el receiver"; exit 1; }

Xvfb "$DISPLAY_NUM" -screen 0 1280x800x24 >/dev/null 2>&1 &
XVFB_PID=$!
sleep 1
DISPLAY="$DISPLAY_NUM" xdotool getdisplaygeometry >/dev/null 2>&1 ||
	{ say "Xvfb no levantó"; exit 1; }
ok "display virtual arriba"

python3 "$ROOT/tests/stub-whisper-server.py" "$PORT" "$EXPECTED" &
STUB_PID=$!
sleep 0.5

# Config propia: tecla sin modificadores para poder sintetizarla, y el texto al
# portapapeles, que es lo que el test puede leer sin una ventana que reciba.
export XDG_CONFIG_HOME="$WORK/config"
export XDG_STATE_HOME="$WORK/state"
mkdir -p "$XDG_CONFIG_HOME/dictador"
cat > "$XDG_CONFIG_HOME/dictador/config.toml" <<EOF
[hotkey]
key = "F9"
mode = "hold"
hold_threshold_ms = 120
cancel_on_other_key = true

[stt]
engine = "faster-whisper"
whisper_server_url = "http://127.0.0.1:$PORT"
partial_interval_ms = 0

[action]
on_release = "clipboard"

[overlay]
enabled = false

[limits]
min_seconds = 0.2
EOF

DISPLAY="$DISPLAY_NUM" "$WORK/dictador" run -v > "$WORK/daemon.log" 2>&1 &
DAEMON_PID=$!
sleep 2

kill -0 "$DAEMON_PID" 2>/dev/null || {
	say "el daemon no arrancó:"
	sed 's/^/    /' "$WORK/daemon.log"
	exit 1
}
grep -q "dictador escuchando" "$WORK/daemon.log" && ok "el daemon escucha la tecla" ||
	bad "el daemon no anunció la tecla"

# El dictado: mantener F9 un rato y soltarla, como haría una persona.
DISPLAY="$DISPLAY_NUM" xdotool keydown F9
sleep 1.2
DISPLAY="$DISPLAY_NUM" xdotool keyup F9
sleep 3

grep -q "grabando" "$WORK/daemon.log" && ok "detectó el press y empezó a grabar" ||
	bad "no detectó el press (¿XInput2 raw?)"

GOT="$(DISPLAY="$DISPLAY_NUM" xclip -selection clipboard -o 2>/dev/null)"
if [[ "$GOT" == "$EXPECTED" ]]; then
	ok "el texto llegó al portapapeles: $GOT"
else
	bad "el portapapeles dice «$GOT», esperaba «$EXPECTED»"
fi

GOT_PRIMARY="$(DISPLAY="$DISPLAY_NUM" xclip -selection primary -o 2>/dev/null)"
[[ "$GOT_PRIMARY" == "$EXPECTED" ]] && ok "la selección primaria también" ||
	bad "la selección primaria dice «$GOT_PRIMARY»"

# El historial tiene que haber registrado el dictado.
if grep -q "$EXPECTED" "$XDG_STATE_HOME/dictador/history.jsonl" 2>/dev/null; then
	ok "quedó en el historial"
else
	bad "no quedó en el historial"
fi

# Una pulsación corta es la tecla usada como tecla: no puede dictar.
DISPLAY="$DISPLAY_NUM" xdotool key F9
sleep 1.5
LINES="$(grep -c "grabando" "$WORK/daemon.log")"
[[ "$LINES" == "1" ]] && ok "un toque corto no dicta" ||
	bad "el toque corto disparó un dictado ($LINES grabaciones)"

# ---- el pegado, que es la parte que más se rompe -------------------------
#
# Hasta acá el texto llegó al portapapeles. Falta lo otro: que el daemon
# devuelva el foco a la ventana que estabas usando y que el Ctrl+V sintético
# llegue de verdad. Para eso hay una ventana que espera el pegado y escribe lo
# que recibe.
say ""
say "y ahora el pegado"

kill "$DAEMON_PID" 2>/dev/null
wait "$DAEMON_PID" 2>/dev/null
sed -i 's/on_release = "clipboard"/on_release = "paste"/' \
	"$XDG_CONFIG_HOME/dictador/config.toml"

DISPLAY="$DISPLAY_NUM" "$WORK/receiver" "$WORK/pegado.txt" dictador-receiver \
	> "$WORK/receiver.log" 2>&1 &
RECEIVER_PID=$!
sleep 1

DISPLAY="$DISPLAY_NUM" "$WORK/dictador" run -v > "$WORK/daemon2.log" 2>&1 &
DAEMON_PID=$!
sleep 2

DISPLAY="$DISPLAY_NUM" xdotool keydown F9
sleep 1.2
DISPLAY="$DISPLAY_NUM" xdotool keyup F9
sleep 4

if [[ "$(cat "$WORK/pegado.txt" 2>/dev/null)" == "$EXPECTED" ]]; then
	ok "el Ctrl+V llegó a la ventana y trajo el texto"
else
	bad "la ventana recibió «$(cat "$WORK/pegado.txt" 2>/dev/null)»"
	sed 's/^/    receiver: /' "$WORK/receiver.log"
	sed 's/^/    daemon: /' "$WORK/daemon2.log"
fi
kill "$RECEIVER_PID" 2>/dev/null

if [[ "$FAILED" != "0" ]]; then
	say ""
	say "log del daemon:"
	sed 's/^/    /' "$WORK/daemon.log"
fi
exit "$FAILED"
