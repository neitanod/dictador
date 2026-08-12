#!/usr/bin/env bash
# El overlay dibujado, sobre un display virtual.
#
# No verifica que quede lindo —eso se mira con el ojo, y para eso están las
# capturas que deja en el directorio de salida— sino que efectivamente dibuje:
# que la ventana se abra, que el cuadro llegue a la pantalla y que cada estado
# se vea distinto del anterior. Un overlay que no dibuja nada deja la pantalla
# negra, y eso sí se puede medir.
set -uo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
DISPLAY_NUM="${OVERLAY_DISPLAY:-:97}"
OUT="${1:-$(mktemp -d)}"
FAILED=0

ok() { printf '  ✓ %s\n' "$*"; }
bad() {
	printf '  ✗ %s\n' "$*"
	FAILED=1
}

cleanup() {
	[[ -n "${XVFB_PID:-}" ]] && kill "$XVFB_PID" 2>/dev/null
	wait 2>/dev/null
}
trap cleanup EXIT

for binary in Xvfb import identify; do
	command -v "$binary" >/dev/null 2>&1 || {
		echo "falta $binary: no puedo probar el overlay"
		exit 77
	}
done

( cd "$ROOT" && go build -o "$OUT/shots" ./tests/shots ) || { echo "no compiló"; exit 1; }

Xvfb "$DISPLAY_NUM" -screen 0 1280x800x24 >/dev/null 2>&1 &
XVFB_PID=$!
sleep 1

if ! DISPLAY="$DISPLAY_NUM" "$OUT/shots" "$OUT" > "$OUT/shots.log" 2>&1; then
	bad "el overlay no abrió"
	sed 's/^/    /' "$OUT/shots.log"
	exit 1
fi
ok "el overlay abre en una pantalla de 24 bits"

# Una pantalla negra tiene media 0: si el cuadro llegó, sube.
previous=""
for shot in "$OUT"/0*.png; do
	name="$(basename "$shot" .png)"
	mean="$(identify -format '%[fx:mean]' "$shot")"
	if awk -v m="$mean" 'BEGIN { exit !(m > 0.002) }'; then
		ok "$name dibujó algo (media $mean)"
	else
		bad "$name salió en negro (media $mean)"
	fi
	if [[ -n "$previous" ]] && cmp -s "$previous" "$shot"; then
		bad "$name salió idéntica a la anterior: el estado no se refleja"
	fi
	previous="$shot"
done

echo "  capturas en $OUT"
exit "$FAILED"
