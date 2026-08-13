// Package desktop pone al dictador donde se lo pueda clickear: un ícono en el
// escritorio, una entrada en el menú y el autostart del login.
//
// Los tres son el mismo archivo —un .desktop de freedesktop.org— en tres
// carpetas distintas, y eso es lo que hace que esto sea un paquete y no tres
// funciones sueltas: lo que cambia entre KDE, GNOME y Xfce no es el formato,
// que es el mismo hace veinte años, sino qué hay que hacer *además* de
// escribirlo para que el escritorio lo muestre como un lanzador y no como un
// archivo de texto con nombre raro.
package desktop

import (
	_ "embed"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

//go:embed icon.svg
var iconSVG []byte

// IconSVG es el dibujo del dictador, para el que quiera mostrarlo sin instalar
// nada: la configuración web lo sirve como favicon y lo pone arriba del título.
// Vive acá y no en una copia al lado de la página porque un ícono que se ve en
// la pantalla distinto del que aparece en el escritorio es peor que no tener
// ninguno de los dos.
func IconSVG() []byte {
	return iconSVG
}

// fileName es el nombre del .desktop en todas las carpetas donde va. Es el
// mismo a propósito: el escritorio identifica una aplicación por ese nombre, y
// con dos distintos el menú y el autostart aparecerían como dos programas.
const fileName = "dictador.desktop"

// iconName es el ícono que pide el .desktop. Es un nombre y no una ruta porque
// así lo resuelve el tema del escritorio en el tamaño que necesite; la ruta
// fija devuelve siempre el mismo dibujo estirado.
const iconName = "dictador"

// fallbackIcon es el del tema, para cuando no se pudo copiar el nuestro. Está
// en cualquier escritorio, así que un lanzador sin ícono propio igual se ve.
const fallbackIcon = "audio-input-microphone"

// Result es lo que quedó escrito después de instalar.
type Result struct {
	Desktop  string   `json:"desktop"`
	Shortcut string   `json:"shortcut"`
	Launcher string   `json:"launcher"`
	Icon     string   `json:"icon,omitempty"`
	Binary   string   `json:"binary"`
	Trusted  bool     `json:"trusted"`
	Notes    []string `json:"notes,omitempty"`
}

// State es lo que hay puesto ahora mismo.
type State struct {
	Desktop   string `json:"desktop"`
	Installed bool   `json:"installed"`
	Shortcut  string `json:"shortcut"`
	Launcher  string `json:"launcher"`
	DirExists bool   `json:"dir_exists"`
}

// run es cómo se corren los programas de afuera —gio, xdg-user-dir— y es una
// variable para que las pruebas puedan ver qué se les pidió sin tocar la
// máquina del que las corre.
var run = func(name string, args ...string) ([]byte, error) {
	return exec.Command(name, args...).CombinedOutput()
}

// Name es el escritorio que está corriendo, con el nombre que usa la gente.
//
// XDG_CURRENT_DESKTOP es la respuesta correcta y a veces trae varios separados
// por dos puntos —"ubuntu:GNOME"— porque las distros se agregan adelante; el
// último es el escritorio de verdad. DESKTOP_SESSION queda de respaldo para las
// sesiones viejas que no ponen la primera.
func Name() string {
	raw := os.Getenv("XDG_CURRENT_DESKTOP")
	if raw == "" {
		raw = os.Getenv("DESKTOP_SESSION")
	}
	if raw == "" {
		return ""
	}
	parts := strings.Split(raw, ":")
	last := strings.TrimSpace(parts[len(parts)-1])
	return prettyName(last)
}

// prettyName traduce lo que dice la variable a lo que diría el usuario.
func prettyName(raw string) string {
	key := strings.ToLower(strings.TrimSpace(raw))
	// Las sesiones se nombran solas con sufijos que no son parte del escritorio:
	// "plasmax11", "gnome-xorg", "ubuntu-wayland".
	for _, suffix := range []string{"x11", "-xorg", "-wayland", "wayland"} {
		key = strings.TrimSuffix(key, suffix)
	}
	key = strings.TrimSuffix(key, "-")
	switch key {
	case "kde", "plasma":
		return "KDE Plasma"
	case "gnome", "gnome-classic", "ubuntu", "unity":
		return "GNOME"
	case "xfce":
		return "Xfce"
	case "x-cinnamon", "cinnamon":
		return "Cinnamon"
	case "mate":
		return "MATE"
	case "lxqt":
		return "LXQt"
	case "lxde":
		return "LXDE"
	case "budgie", "budgie-desktop":
		return "Budgie"
	case "deepin", "dde":
		return "Deepin"
	case "pantheon":
		return "Pantheon"
	case "":
		return ""
	default:
		return raw
	}
}

// needsTrust dice si este escritorio pide que el lanzador esté marcado como
// confiable antes de obedecer un doble click.
//
// Es cosa de los administradores de archivos de la familia de Nautilus —Nemo y
// Caja son sus hijos, y el escritorio de GNOME hoy lo dibuja una extensión que
// hereda la misma regla—: un .desktop que nadie marcó se muestra con el nombre
// del archivo y un cartel de "lanzador no confiable". KDE y Xfce se conforman
// con el bit de ejecución, que se lo ponemos a todos igual.
func needsTrust(name string) bool {
	switch name {
	case "GNOME", "Cinnamon", "MATE", "Budgie", "Pantheon":
		return true
	default:
		return false
	}
}

// Binary es el dictador que hay que escribir en el Exec.
//
// El que está corriendo es el candidato natural y alcanza casi siempre. Falla
// justo en el caso en que uno prueba esto: compilado a un temporal por `go
// run`, el Exec quedaría apuntando a un archivo que el sistema va a borrar. Por
// eso, si el que corre no se llama dictador o vive en un temporal, gana el que
// esté instalado en el PATH.
func Binary() string {
	self, err := os.Executable()
	if err == nil {
		if resolved, err := filepath.EvalSymlinks(self); err == nil {
			self = resolved
		}
		if plausible(self) {
			return self
		}
	}
	if found, err := exec.LookPath("dictador"); err == nil {
		if resolved, err := filepath.EvalSymlinks(found); err == nil {
			return resolved
		}
		return found
	}
	if self != "" {
		return self
	}
	return "dictador"
}

func plausible(path string) bool {
	if filepath.Base(path) != "dictador" {
		return false
	}
	temp := filepath.Clean(os.TempDir()) + string(os.PathSeparator)
	return !strings.HasPrefix(filepath.Clean(path), temp)
}

// Entry es el contenido del .desktop.
//
// El autostart lleva una línea de más y ninguna categoría: en el menú no va, y
// GNOME quiere que le digan explícitamente que sí a lo que arranca solo.
func Entry(binary, icon string, autostart bool) string {
	if icon == "" {
		icon = fallbackIcon
	}
	entry := "[Desktop Entry]\n" +
		"Type=Application\n" +
		"Version=1.0\n" +
		"Name=Dictador\n" +
		"GenericName=Dictado por voz\n" +
		"Comment=Mantené la tecla, hablá, soltá: el texto entra donde estabas escribiendo\n" +
		// --notify porque al que hace doble click no le queda ninguna otra
		// manera de enterarse: el dictador arranca sin ventana y sin terminal.
		"Exec=" + quote(binary) + " run --notify\n" +
		"Icon=" + icon + "\n" +
		"Terminal=false\n" +
		// Sin esto el escritorio pone el cursor en "esperá" y una entrada
		// fantasma en la barra de tareas durante medio minuto, esperando una
		// ventana que este programa no va a abrir nunca.
		"StartupNotify=false\n"
	if autostart {
		return entry + "X-GNOME-Autostart-enabled=true\n"
	}
	return entry +
		"Categories=Utility;Accessibility;\n" +
		"Keywords=dictado;dictar;voz;hablar;speech;dictation;microfono;\n"
}

// quote entrecomilla la ruta si hace falta. El Exec del .desktop se parte por
// espacios, así que un dictador guardado en una carpeta con espacios se
// entiende como dos argumentos y no arranca.
func quote(binary string) string {
	if strings.ContainsAny(binary, " \t\"'") {
		return `"` + strings.ReplaceAll(binary, `"`, `\"`) + `"`
	}
	return binary
}

// Dir es la carpeta del escritorio de este usuario.
//
// "~/Desktop" es la respuesta más común y la peor de las primeras: en una
// sesión en castellano la carpeta se llama Escritorio, y escribir el ícono en
// la otra lo manda a un lugar que el usuario no mira nunca. El orden va de lo
// que el sistema afirma a lo que uno adivina.
func Dir() (string, error) {
	if dir := expand(os.Getenv("XDG_DESKTOP_DIR")); dir != "" {
		return dir, nil
	}
	if dir := desktopFromUserDirs(); dir != "" {
		return dir, nil
	}
	if out, err := run("xdg-user-dir", "DESKTOP"); err == nil {
		if dir := expand(strings.TrimSpace(string(out))); dir != "" && dir != home() {
			return dir, nil
		}
	}
	for _, name := range []string{"Desktop", "Escritorio", "Área de Trabalho", "Bureau", "Schreibtisch"} {
		candidate := filepath.Join(home(), name)
		if info, err := os.Stat(candidate); err == nil && info.IsDir() {
			return candidate, nil
		}
	}
	return "", errors.New("no encontré la carpeta de tu escritorio: probá `xdg-user-dirs-update` o creá ~/Desktop")
}

// desktopFromUserDirs lee la línea del escritorio del archivo donde el sistema
// guarda cómo se llaman las carpetas del usuario.
func desktopFromUserDirs() string {
	data, err := os.ReadFile(filepath.Join(configHome(), "user-dirs.dirs"))
	if err != nil {
		return ""
	}
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "XDG_DESKTOP_DIR=") {
			continue
		}
		value := strings.TrimPrefix(line, "XDG_DESKTOP_DIR=")
		value = strings.Trim(strings.TrimSpace(value), `"`)
		return expand(value)
	}
	return ""
}

// expand resuelve el $HOME que el archivo de carpetas escribe sin resolver.
func expand(path string) string {
	path = strings.TrimSpace(path)
	if path == "" {
		return ""
	}
	if strings.HasPrefix(path, "$HOME") {
		return filepath.Join(home(), strings.TrimPrefix(path, "$HOME"))
	}
	if strings.HasPrefix(path, "~") {
		return filepath.Join(home(), strings.TrimPrefix(path, "~"))
	}
	if !filepath.IsAbs(path) {
		return ""
	}
	return filepath.Clean(path)
}

func home() string {
	if dir, err := os.UserHomeDir(); err == nil {
		return dir
	}
	return "."
}

func configHome() string {
	if dir := os.Getenv("XDG_CONFIG_HOME"); dir != "" {
		return dir
	}
	return filepath.Join(home(), ".config")
}

func dataHome() string {
	if dir := os.Getenv("XDG_DATA_HOME"); dir != "" {
		return dir
	}
	return filepath.Join(home(), ".local", "share")
}

// LauncherPath es el .desktop del menú de aplicaciones.
func LauncherPath() string {
	return filepath.Join(dataHome(), "applications", fileName)
}

// IconPath es dónde va nuestro SVG para que el tema lo encuentre por nombre.
func IconPath() string {
	return filepath.Join(dataHome(), "icons", "hicolor", "scalable", "apps", iconName+".svg")
}

// AutostartPath es el .desktop que arranca el dictado en cada login.
func AutostartPath() string {
	return filepath.Join(configHome(), "autostart", fileName)
}

// Install pone el ícono en el escritorio y la entrada en el menú.
//
// Van juntos porque son la misma pregunta contestada dos veces: el que quiere
// el ícono en el escritorio también quiere encontrarlo escribiendo "dictador"
// en el menú, y la entrada del menú es además la que le da al escritorio el
// nombre y el ícono que después muestra en la barra de tareas.
func Install(binary string) (Result, error) {
	name := Name()
	result := Result{Desktop: name, Binary: binary}

	dir, err := Dir()
	if err != nil {
		return result, err
	}

	icon := iconName
	if err := writeIcon(); err != nil {
		icon = fallbackIcon
		result.Notes = append(result.Notes,
			"no pude copiar el ícono ("+err.Error()+"), va con el del sistema")
	} else {
		result.Icon = IconPath()
	}
	entry := Entry(binary, icon, false)

	result.Launcher = LauncherPath()
	if err := write(result.Launcher, entry, 0o644); err != nil {
		return result, err
	}
	// El menú lee un índice, y hasta que se rearme el lanzador nuevo no aparece
	// en la búsqueda. Que falte el programa que lo rearma no es un problema: el
	// archivo ya está, y el índice se rehace solo en el próximo login.
	if _, err := exec.LookPath("update-desktop-database"); err == nil {
		_, _ = run("update-desktop-database", filepath.Dir(result.Launcher))
	}

	result.Shortcut = filepath.Join(dir, fileName)
	// El bit de ejecución es lo que separa un lanzador de un archivo de texto:
	// sin él, KDE ofrece abrirlo con un editor y GNOME lo ignora.
	if err := write(result.Shortcut, entry, 0o755); err != nil {
		return result, err
	}
	if needsTrust(name) {
		if err := trust(result.Shortcut); err != nil {
			result.Notes = append(result.Notes,
				"no pude marcarlo como confiable ("+err.Error()+"): si el escritorio "+
					"lo muestra como \"lanzador no confiable\", botón derecho → Permitir ejecutar")
		} else {
			result.Trusted = true
		}
	}
	return result, nil
}

// trust le dice al escritorio que este lanzador lo puso el usuario a propósito.
func trust(path string) error {
	if _, err := exec.LookPath("gio"); err != nil {
		return errors.New("no está gio")
	}
	if out, err := run("gio", "set", path, "metadata::trusted", "true"); err != nil {
		message := strings.TrimSpace(string(out))
		if message == "" {
			message = err.Error()
		}
		return errors.New(message)
	}
	return nil
}

// Uninstall saca el ícono y la entrada del menú. El autostart no se toca: son
// decisiones distintas y el que saca el ícono del escritorio no está pidiendo
// que el dictado deje de arrancar en el login.
func Uninstall() (Result, error) {
	result := Result{Desktop: Name(), Launcher: LauncherPath()}
	if dir, err := Dir(); err == nil {
		result.Shortcut = filepath.Join(dir, fileName)
	}
	var last error
	for _, path := range []string{result.Shortcut, result.Launcher} {
		if path == "" {
			continue
		}
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			last = err
		}
	}
	return result, last
}

// Status cuenta si el ícono está puesto.
func Status() State {
	state := State{Desktop: Name(), Launcher: LauncherPath()}
	dir, err := Dir()
	if err == nil {
		state.DirExists = true
		state.Shortcut = filepath.Join(dir, fileName)
		if _, err := os.Stat(state.Shortcut); err == nil {
			state.Installed = true
		}
	}
	return state
}

// InstallAutostart hace que el dictado arranque solo en cada login.
//
// Es el mismo .desktop del ícono con una línea de más, en la carpeta que el
// escritorio mira al iniciar sesión. Que sea el mismo archivo es la gracia:
// arreglar el lanzador lo arregla en los dos lados.
func InstallAutostart(binary string) (string, error) {
	path := AutostartPath()
	return path, write(path, Entry(binary, EnsureIcon(), true), 0o644)
}

// RemoveAutostart lo saca. Que no esté no es un error: el que lo destilda
// quiere que no arranque solo, y no arranca solo.
func RemoveAutostart() error {
	if err := os.Remove(AutostartPath()); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

// AutostartInstalled dice si el dictado arranca solo.
func AutostartInstalled() bool {
	_, err := os.Stat(AutostartPath())
	return err == nil
}

// EnsureIcon deja el ícono copiado y devuelve el nombre que hay que escribir en
// el .desktop: el nuestro si se pudo, el del tema si no. Nunca falla, porque un
// lanzador con el ícono del sistema es mejor que no tener lanzador.
func EnsureIcon() string {
	if err := writeIcon(); err != nil {
		return fallbackIcon
	}
	return iconName
}

func writeIcon() error {
	return writeBytes(IconPath(), iconSVG, 0o644)
}

func write(path, content string, mode os.FileMode) error {
	return writeBytes(path, []byte(content), mode)
}

func writeBytes(path string, content []byte, mode os.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("no pude crear %s: %w", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, content, mode); err != nil {
		return fmt.Errorf("no pude escribir %s: %w", path, err)
	}
	// WriteFile respeta el modo sólo cuando crea el archivo: reinstalar encima
	// de uno que quedó sin el bit de ejecución lo dejaría sin él para siempre.
	return os.Chmod(path, mode)
}
