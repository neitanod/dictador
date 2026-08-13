package config

import (
	"fmt"
	"os"
	"sync"
	"time"
)

// Watcher mira el config.toml y avisa cuando alguien lo cambió.
//
// Va preguntando por la fecha y el tamaño en vez de usar inotify: son dos
// llamadas por segundo sobre un archivo que ya está en el cache del kernel, y
// se ahorra una dependencia entera. inotify además obliga a re-mirar el archivo
// después de cada guardado, porque los editores que escriben aparte y renombran
// dejan el watch apuntando a un inode que ya no existe.
type Watcher struct {
	changed chan struct{}
	stop    chan struct{}
	once    sync.Once
}

// NewWatcher arranca a mirar el archivo cada tanto.
//
// Cómo está el archivo ahora se anota acá y no adentro de la goroutine: si se
// anotara allá, un cambio hecho apenas vuelve esta función podría llegar antes
// que la primera mirada y quedar tomado como el estado de siempre.
func NewWatcher(path string, every time.Duration) *Watcher {
	w := &Watcher{
		changed: make(chan struct{}, 1),
		stop:    make(chan struct{}),
	}
	go w.run(path, every, stamp(path))
	return w
}

// Changed avisa cada vez que el archivo terminó de cambiar.
func (w *Watcher) Changed() <-chan struct{} { return w.changed }

// Close deja de mirar.
func (w *Watcher) Close() { w.once.Do(func() { close(w.stop) }) }

func (w *Watcher) run(path string, every time.Duration, quieto string) {
	tick := time.NewTicker(every)
	defer tick.Stop()

	// moviéndose es un cambio recién visto, que todavía no se confirmó.
	var moviéndose string
	for {
		select {
		case <-w.stop:
			return
		case <-tick.C:
		}
		ahora := stamp(path)
		switch {
		case ahora == quieto:
			// Volvió a lo de antes: un archivo a medio guardar que ya terminó.
			moviéndose = ""
		case ahora != moviéndose:
			// Cambió recién. Se espera una vuelta más para avisar, porque el
			// editor puede estar guardando todavía y el archivo estar a mitad.
			moviéndose = ahora
		default:
			quieto, moviéndose = ahora, ""
			select {
			case w.changed <- struct{}{}:
			default: // ya hay un aviso esperando, y con uno alcanza
			}
		}
	}
}

// stamp es lo que se compara: fecha y tamaño, o vacío si el archivo no está.
func stamp(path string) string {
	info, err := os.Stat(path)
	if err != nil {
		return ""
	}
	return fmt.Sprintf("%d/%d", info.ModTime().UnixNano(), info.Size())
}
