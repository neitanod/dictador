package cli

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
)

// El candado que impide dos dictadores a la vez.
//
// Hace falta desde que hay un ícono. La tecla se escucha por XInput2 crudo y sin
// grab —a propósito, así sigue funcionando como modificador—, y eso tiene una
// consecuencia que la terminal escondía: dos procesos escuchando la misma tecla
// graban los dos y pegan los dos, y el texto sale duplicado sin ninguna pista de
// por qué. En la terminal uno se acuerda de que ya lo arrancó; frente a un ícono
// que no muestra ninguna ventana, volver a hacerle doble click es lo natural.

// lockPath es el archivo del candado.
//
// Va en el directorio de runtime, que el sistema vacía al cerrar sesión: un
// candado que sobreviva al reboot es un candado que traba el arranque siguiente.
// Si no hay —una sesión sin systemd, un contenedor pelado—, /tmp con el uid
// adentro del nombre, para que dos usuarios en la misma máquina no compartan el
// candado de uno.
func lockPath() string {
	if dir := os.Getenv("XDG_RUNTIME_DIR"); dir != "" {
		return filepath.Join(dir, "dictador.lock")
	}
	return filepath.Join(os.TempDir(), fmt.Sprintf("dictador-%d.lock", os.Getuid()))
}

// lock es el candado tomado, y sirve para soltarlo.
type lock struct {
	file *os.File
}

func (l *lock) release() {
	if l == nil || l.file == nil {
		return
	}
	_ = syscall.Flock(int(l.file.Fd()), syscall.LOCK_UN)
	_ = l.file.Close()
}

// takeLock intenta quedarse con el candado.
//
// El flock lo suelta el kernel cuando el proceso muere, con lo que un dictador
// que se cayó de mala manera no deja el candado puesto. El PID escrito adentro
// no es lo que cierra la puerta: es para que el mensaje pueda decir quién la
// tiene.
func takeLock() (*lock, int, error) {
	path := lockPath()
	file, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0o644)
	if err != nil {
		// Sin candado se sigue igual: no poder escribir un archivo en /tmp no
		// es motivo para no dejar dictar.
		return nil, 0, err
	}
	if err := syscall.Flock(int(file.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		other := readPID(file)
		_ = file.Close()
		return nil, other, errBusy
	}
	if err := file.Truncate(0); err == nil {
		_, _ = file.WriteAt([]byte(strconv.Itoa(os.Getpid())+"\n"), 0)
	}
	return &lock{file: file}, 0, nil
}

// errBusy es "ya hay uno corriendo", que no es un error de nada: es la respuesta.
var errBusy = fmt.Errorf("ya hay un dictador andando")

func readPID(file *os.File) int {
	buf := make([]byte, 32)
	n, _ := file.ReadAt(buf, 0)
	pid, err := strconv.Atoi(strings.TrimSpace(string(buf[:n])))
	if err != nil {
		return 0
	}
	return pid
}

// notify manda una notificación del escritorio, si la máquina tiene con qué.
//
// Es lo que le falta a un programa que a propósito no se ve por ningún lado:
// cuando lo prendés desde un ícono, alguna señal de que arrancó. Se manda sólo
// con --notify, que es lo que pone el .desktop: desde la terminal ya está el
// texto que imprime, y dos avisos de lo mismo es uno de más.
func notify(title, body string) {
	found, err := exec.LookPath("notify-send")
	if err != nil {
		return
	}
	cmd := exec.Command(found,
		"--app-name=Dictador",
		"--icon=dictador",
		// Expira sola: es un aviso de arranque, no algo para leer después.
		"--expire-time=4000",
		title, body)
	if err := cmd.Start(); err != nil {
		return
	}
	go func() { _ = cmd.Wait() }()
}
