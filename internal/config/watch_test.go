package config

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

const paso = 20 * time.Millisecond

func TestElWatcherAvisaCuandoElArchivoCambia(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	write(t, path, "[stt]\nengine = \"chrome\"\n")

	w := NewWatcher(path, paso)
	defer w.Close()

	write(t, path, "[stt]\nengine = \"google\"\n")
	esperarAviso(t, w, "el cambio en el archivo no llegó")
}

func TestElWatcherEsperaAQueElArchivoSeQuedeQuieto(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	write(t, path, "uno\n")

	w := NewWatcher(path, paso)
	defer w.Close()

	// Un editor que guarda en dos pasos —vaciar y escribir— pasa por un archivo
	// a medias. Avisar ahí daría un TOML roto que no es el que el usuario dejó.
	write(t, path, "")
	time.Sleep(paso)
	write(t, path, "dos\n")

	esperarAviso(t, w, "el cambio no llegó nunca")
	// Y un solo aviso por un solo guardado: lo de arriba es una edición, no dos.
	select {
	case <-w.Changed():
		t.Error("un guardado en dos pasos avisó dos veces")
	case <-time.After(10 * paso):
	}
}

func TestElWatcherAvisaCuandoElArchivoAparece(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")

	w := NewWatcher(path, paso)
	defer w.Close()

	// Sin config.toml el daemon anda con los defaults; que aparezca uno es
	// exactamente el momento en que hay algo nuevo para leer.
	write(t, path, "[stt]\nengine = \"chrome\"\n")
	esperarAviso(t, w, "el config que apareció pasó desapercibido")
}

func TestElWatcherSeQuedaCalladoSiNadieTocaNada(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	write(t, path, "[stt]\nengine = \"chrome\"\n")

	w := NewWatcher(path, paso)
	defer w.Close()

	select {
	case <-w.Changed():
		t.Error("avisó un cambio que nadie hizo")
	case <-time.After(10 * paso):
	}
}

func TestCerrarElWatcherDosVecesNoRompe(t *testing.T) {
	w := NewWatcher(filepath.Join(t.TempDir(), "config.toml"), paso)
	w.Close()
	w.Close()
}

func esperarAviso(t *testing.T, w *Watcher, queja string) {
	t.Helper()
	select {
	case <-w.Changed():
	case <-time.After(2 * time.Second):
		t.Fatal(queja)
	}
}

func write(t *testing.T, path, body string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("no pude escribir %s: %v", path, err)
	}
}
