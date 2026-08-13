#!/usr/bin/env bash
# El barrido de Chrome huérfanos, con Chrome de verdad: un dictador que muere de
# un kill -9 y otro que arranca después y tiene que limpiar lo que quedó.
#
# Los tests de Go prueban a quién matamos y a quién no contra un /proc armado a
# mano. Lo que sólo se puede ver acá es que un Chrome de verdad, lanzado por un
# dictador de verdad, se deja encontrar y matar: la primera versión de esto no
# encontraba a ninguno, porque Chrome reescribe su línea de comandos y en /proc
# no queda separada por NUL como la de todo el resto.
#
# Corre entero adentro de un TMPDIR propio, así que no puede tocar el /tmp de la
# máquina ni el Chrome del dictador que estés usando.
set -uo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
DISPLAY_NUM="${ORPHANS_DISPLAY:-:98}"
WORK="$(mktemp -d)"
FAILED=0

ok() { printf '  ✓ %s\n' "$*"; }
bad() {
	printf '  ✗ %s\n' "$*"
	FAILED=1
}

need() {
	for binary in "$@"; do
		command -v "$binary" >/dev/null 2>&1 || {
			echo "falta $binary: no puedo correr la prueba del barrido"
			exit 77
		}
	done
}

cleanup() {
	[[ -n "${A_PID:-}" ]] && kill -9 "$A_PID" 2>/dev/null
	[[ -n "${B_PID:-}" ]] && kill "$B_PID" 2>/dev/null
	[[ -n "${C_PID:-}" ]] && kill "$C_PID" 2>/dev/null
	sleep 1
	pkill -9 -f "user-data-dir=$WORK/tmp/dictador-chrome-" 2>/dev/null
	[[ -n "${XVFB_PID:-}" ]] && kill "$XVFB_PID" 2>/dev/null
	wait 2>/dev/null
	rm -rf "$WORK"
}
trap cleanup EXIT

need Xvfb pgrep
command -v google-chrome >/dev/null 2>&1 ||
	command -v chromium >/dev/null 2>&1 || {
	echo "no hay Chrome en esta máquina: no puedo correr la prueba del barrido"
	exit 77
}

echo "barrido de huérfanos en $DISPLAY_NUM"

(cd "$ROOT" && go build -o "$WORK/dictador" .) || {
	echo "no compiló"
	exit 1
}

Xvfb "$DISPLAY_NUM" -screen 0 1280x800x24 >/dev/null 2>&1 &
XVFB_PID=$!
sleep 1

export TMPDIR="$WORK/tmp"
export XDG_CONFIG_HOME="$WORK/config"
export XDG_STATE_HOME="$WORK/state"
export DISPLAY="$DISPLAY_NUM"
mkdir -p "$TMPDIR" "$XDG_CONFIG_HOME/dictador"
cat > "$XDG_CONFIG_HOME/dictador/config.toml" <<EOF
[hotkey]
key = "F9"

[stt]
engine = "chrome"
chrome_headless = true

[overlay]
enabled = false
EOF

perfiles() { ls -d "$TMPDIR"/dictador-chrome-* 2>/dev/null; }
chromes() { pgrep -f "user-data-dir=$TMPDIR/dictador-chrome-" 2>/dev/null; }
esperar() { # esperar <segundos> <comando…>: hasta que dé bien
	local limite=$(($1 * 2))
	shift
	for _ in $(seq "$limite"); do
		"$@" && return 0
		sleep 0.5
	done
	return 1
}

# --- el dictador que se va a morir mal ------------------------------------
"$WORK/dictador" run -v > "$WORK/a.log" 2>&1 &
A_PID=$!
esperar 30 test -n "$(chromes)"

PERFIL_A="$(perfiles | head -1)"
CHROME_A="$(chromes | head -1)"
if [[ -z "$CHROME_A" ]]; then
	bad "el primer dictador no levantó Chrome"
	sed 's/^/    /' "$WORK/a.log"
	exit 1
fi
ok "el primer dictador levantó su Chrome (pid $CHROME_A)"

grep -q "^$A_PID " "$PERFIL_A/dictador.owner" 2>/dev/null &&
	ok "el perfil quedó firmado por el dictador ($(cat "$PERFIL_A/dictador.owner"))" ||
	bad "el perfil no quedó firmado por el dictador $A_PID"

kill -9 "$A_PID"
sleep 1
kill -0 "$CHROME_A" 2>/dev/null &&
	ok "tras el kill -9 el Chrome quedó huérfano y vivo" ||
	bad "el Chrome se fue solo: la prueba no mide nada"

# --- el dictador que llega después ----------------------------------------
"$WORK/dictador" run -v > "$WORK/b.log" 2>&1 &
B_PID=$!
esperar 30 grep -q "huérfanos" "$WORK/b.log"

kill -0 "$CHROME_A" 2>/dev/null &&
	bad "el Chrome huérfano sigue vivo (pid $CHROME_A)" ||
	ok "el segundo dictador mató al Chrome huérfano"
[[ -d "$PERFIL_A" ]] &&
	bad "el perfil huérfano quedó en $PERFIL_A" ||
	ok "y borró su perfil"
grep -q "huérfanos" "$WORK/b.log" &&
	ok "lo dejó dicho: $(grep 'huérfanos' "$WORK/b.log")" ||
	bad "el barrido no dejó rastro en el log"

esperar 30 test -n "$(chromes)"
CHROME_B="$(chromes | head -1)"
[[ -n "$CHROME_B" ]] &&
	ok "y levantó su propio Chrome (pid $CHROME_B)" ||
	bad "el segundo dictador se quedó sin Chrome"

# --- y con dos dictadores vivos, ninguno toca al otro ----------------------
"$WORK/dictador" run -v > "$WORK/c.log" 2>&1 &
C_PID=$!
esperar 30 grep -q "escuchando\|motor de voz" "$WORK/c.log"
sleep 2
kill -0 "$CHROME_B" 2>/dev/null &&
	ok "un tercer dictador no le toca el Chrome al segundo, que está vivo" ||
	bad "el tercer dictador le mató el Chrome a un dictador vivo"

exit "$FAILED"
