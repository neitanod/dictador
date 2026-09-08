BINARY  := dictador
PREFIX  ?= $(HOME)/.local
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS := -s -w -X github.com/neitanod/dictador/internal/cli.Version=$(VERSION)

.PHONY: build install link uninstall test e2e fmt vet clean

build:
	go build -ldflags '$(LDFLAGS)' -o bin/$(BINARY) .

# link es install para el que además desarrolla acá: en vez de una copia deja un
# enlace a bin/, y entonces cada `make build` ya es la versión que corre en la
# terminal y la que arranca el ícono. La copia de `install` es lo que querés
# cuando el checkout es de paso y el binario tiene que sobrevivirlo.
link: build
	install -d $(PREFIX)/bin
	@if [ -e $(PREFIX)/bin/$(BINARY) ] && [ ! -L $(PREFIX)/bin/$(BINARY) ]; then \
		echo "había una copia en $(PREFIX)/bin/$(BINARY): la reemplazo por el enlace"; fi
	ln -sfn $(CURDIR)/bin/$(BINARY) $(PREFIX)/bin/$(BINARY)
	@echo "$(PREFIX)/bin/$(BINARY) → $(CURDIR)/bin/$(BINARY)"
	@case ":$$PATH:" in *":$(PREFIX)/bin:"*) ;; \
		*) echo "ojo: $(PREFIX)/bin no está en tu PATH" ;; esac

install: build
	install -d $(PREFIX)/bin
	@if [ -L $(PREFIX)/bin/$(BINARY) ]; then \
		echo "ojo: había un enlace a este checkout y queda una copia congelada acá; para volver, make link"; fi
	install -m 0755 bin/$(BINARY) $(PREFIX)/bin/$(BINARY)
	@echo "$(BINARY) instalado en $(PREFIX)/bin"
	@case ":$$PATH:" in *":$(PREFIX)/bin:"*) ;; \
		*) echo "ojo: $(PREFIX)/bin no está en tu PATH" ;; esac

uninstall:
	rm -f $(PREFIX)/bin/$(BINARY)

test:
	go test ./...

e2e:
	bash tests/e2e.sh
	bash tests/traduccion.sh

fmt:
	gofmt -w .

vet:
	go vet ./...

clean:
	rm -rf bin
