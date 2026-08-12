BINARY  := dictador
PREFIX  ?= $(HOME)/.local
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS := -s -w -X github.com/neitanod/dictador/internal/cli.Version=$(VERSION)

.PHONY: build install uninstall test e2e fmt vet clean

build:
	go build -ldflags '$(LDFLAGS)' -o bin/$(BINARY) .

install: build
	install -d $(PREFIX)/bin
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

fmt:
	gofmt -w .

vet:
	go vet ./...

clean:
	rm -rf bin
