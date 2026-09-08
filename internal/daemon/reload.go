package daemon

import (
	"reflect"
	"strings"
	"time"

	"github.com/neitanod/dictador/internal/config"
	"github.com/neitanod/dictador/internal/overlay"
)

// reloadPlan es lo que hay que hacer cuando el config.toml cambió en disco.
//
// Casi todo el archivo se lee en cada dictado, así que la mayoría de los
// cambios no piden nada: la próxima vez que dictes ya andan. Lo que queda es
// esto: el motor, que hay que rearmar; la ventanita, que hay que mudar; y lo
// que se agarró una sola vez al arrancar y recién cambia cuando reinicies.
type reloadPlan struct {
	Engine    bool
	Placement bool
	Languages bool
	Restart   []string
}

func (p reloadPlan) busy() bool {
	return p.Engine || p.Placement || p.Languages || len(p.Restart) > 0
}

// notice es lo que se le muestra al usuario, o "" si no hay nada que decirle.
func (p reloadPlan) notice() string {
	if len(p.Restart) == 0 {
		return ""
	}
	return "leí el config.toml; " + strings.Join(p.Restart, " y ") +
		" empieza cuando reinicies el dictador"
}

// planReload compara lo que había con lo que dice el archivo ahora.
func planReload(old, next config.Config) reloadPlan {
	var plan reloadPlan
	// La sección entera y no sólo el nombre del motor: el modelo, el idioma o
	// la URL del whisper-server cambian tanto lo que transcribe como el motor.
	plan.Engine = !reflect.DeepEqual(old.STT, next.STT)
	plan.Placement = old.Overlay.Screen != next.Overlay.Screen ||
		old.Overlay.Position != next.Overlay.Position
	// Las letras que eligen idioma se resuelven contra el teclado una vez, así
	// que cambiarlas en el archivo pide volver a resolverlas.
	plan.Languages = old.Translate.Enabled != next.Translate.Enabled ||
		!reflect.DeepEqual(old.Translate.Keys, next.Translate.Keys)

	if old.Hotkey.Key != next.Hotkey.Key {
		plan.Restart = append(plan.Restart, "la tecla nueva")
	}
	if old.Audio != next.Audio {
		plan.Restart = append(plan.Restart, "el micrófono nuevo")
	}
	// Que la ventanita exista o no, y de qué tamaño, se decide al abrirla: lo
	// único que se muda con ella abierta es a qué pantalla y a qué esquina.
	if drawnDiffers(old.Overlay, next.Overlay) {
		plan.Restart = append(plan.Restart, "la ventanita nueva")
	}
	return plan
}

// drawnDiffers dice si cambió algo de la ventanita que se decide al dibujarla.
func drawnDiffers(old, next config.Overlay) bool {
	old.Screen, next.Screen = "", ""
	old.Position, next.Position = "", ""
	old.HideDelayMs, next.HideDelayMs = 0, 0
	return old != next
}

// configPath es el archivo del que salió esta configuración, o el de siempre.
func configPath(cfg config.Config) string {
	if cfg.Path != "" {
		return cfg.Path
	}
	return config.ConfigPath()
}

// partialEvery es cada cuánto se pide el texto en vivo mientras dictás.
func partialEvery(cfg config.Config) time.Duration {
	return time.Duration(cfg.STT.PartialIntervalMs) * time.Millisecond
}

// reloadConfig relee el config.toml y aplica lo que se pueda en caliente.
func (d *Daemon) reloadConfig() {
	path := configPath(d.cfg)
	next, err := config.Load(path)
	if err != nil {
		// Un TOML roto tiene que gritar: lo escribiste vos hace un segundo y el
		// programa está andando con lo de antes sin que se note.
		d.showError("el config.toml tiene un error: " + err.Error())
		return
	}
	next.Path = path

	plan := planReload(d.cfg, next)
	d.cfg = next
	if d.web != nil {
		d.web.Update(d.cfg)
	}
	d.partialEvery = partialEvery(d.cfg)
	if plan.Placement {
		if placeable, ok := d.ui.(overlay.Placeable); ok {
			placeable.SetPlacement(d.cfg.Overlay.Screen, d.cfg.Overlay.Position)
		}
	}
	if plan.Engine {
		d.modelReady = false
		if !d.buildEngine() {
			d.watchLanguageKeys()
			d.showError(d.engErr)
			return
		}
		d.log("motor nuevo: " + d.EngineLine())
		go d.preload()
	}
	// Después del motor: qué letras escuchar depende de si el motor que quedó
	// sabe traducir.
	if plan.Engine || plan.Languages {
		d.watchLanguageKeys()
	}
	d.log("releí " + path)
	if aviso := plan.notice(); aviso != "" {
		d.showError(aviso)
	}
}
