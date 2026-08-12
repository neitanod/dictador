#!/usr/bin/env bash
# Toda la verificación de dictador, en orden: lo barato primero.
set -uo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT" || exit 1
FAILED=0

step() {
	printf '\n\033[1m== %s\033[0m\n' "$1"
	shift
	if "$@"; then
		printf '\033[32mok\033[0m\n'
	else
		printf '\033[31mfalló\033[0m\n'
		FAILED=1
	fi
}

step "gofmt (nada sin formatear)" bash -c '
	pendientes="$(gofmt -l . 2>/dev/null)"
	[[ -z "$pendientes" ]] || { echo "$pendientes"; exit 1; }'
step "go vet" go vet ./...
step "tests unitarios" go test ./...
step "race detector" go test -race ./internal/stt/ ./internal/audio/

printf '\n\033[1m== el overlay dibujado\033[0m\n'
bash "$ROOT/tests/overlay.sh"
case "$?" in
0) printf '\033[32mok\033[0m\n' ;;
77) printf '\033[33msalteado (falta Xvfb o imagemagick)\033[0m\n' ;;
*)
	printf '\033[31mfalló\033[0m\n'
	FAILED=1
	;;
esac

# El e2e necesita un display virtual y no está en todas las máquinas: si falta
# algo se saltea (77) en vez de dar por fallada la suite.
printf '\n\033[1m== end-to-end sobre Xvfb\033[0m\n'
bash "$ROOT/tests/e2e.sh"
case "$?" in
0) printf '\033[32mok\033[0m\n' ;;
77) printf '\033[33msalteado (falta Xvfb, xdotool o xclip)\033[0m\n' ;;
*)
	printf '\033[31mfalló\033[0m\n'
	FAILED=1
	;;
esac

printf '\n'
if [[ "$FAILED" == "0" ]]; then
	printf '\033[32mtodo verde\033[0m\n'
else
	printf '\033[31mhay algo roto\033[0m\n'
fi
exit "$FAILED"
