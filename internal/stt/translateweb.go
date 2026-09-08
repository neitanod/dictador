package stt

import (
	"encoding/json"
	"fmt"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/neitanod/dictador/internal/cdp"
)

// El traductor de la web, manejado como lo maneja una persona: se escribe en el
// cuadro de la izquierda y se lee lo que aparece a la derecha.
//
// Da otra traducción que el endpoint público, y mejor: "Mañana voy a estar
// hecho mierda" sale como "Tomorrow I'm going to be a wreck" en vez de
// "Tomorrow I'm going to be shit". La diferencia es de modelo, y Google decide
// cuál te toca por dos cosas que no se pueden falsear desde afuera: que el
// navegador no diga ser headless en su User-Agent —eso lo arreglamos al
// lanzarlo— y un token antifraude que su JavaScript arma para sus propios
// pedidos.
//
// Cuesta cerca de un segundo con la pestaña ya abierta, contra los trescientos
// milisegundos del endpoint. Por eso el endpoint sigue estando: es la red que
// atrapa el dictado cuando esto falla, y el modo rápido para el que lo
// prefiera.

// translateURL arma la dirección de la página para un idioma destino.
//
// El hl —el idioma en el que Google te habla a vos— va puesto a propósito: con
// la página en inglés, Google sirve el traductor viejo, el que traduce palabra
// por palabra. Con la página en castellano aparece el que traduce por sentido.
func translateURL(target, ui string) string {
	if ui == "" {
		ui = "es-419"
	}
	return "https://translate.google.com/?sl=auto&op=translate&hl=" +
		url.QueryEscape(ui) + "&tl=" + url.QueryEscape(target)
}

// La página se maneja con estas dos referencias, y son lo único frágil de todo
// esto: el cuadro donde se escribe y el nodo donde aparece la traducción. Si
// Google los cambia, la traducción cae al endpoint y el usuario ve el aviso.
const translateScript = `(async () => {
  const texto = %s, limite = %d;
  const entrada = document.querySelector('textarea');
  if (!entrada) return {error: 'no encontré el cuadro de texto de Google'};

  // El resultado puede venir partido en varios pedazos, uno por oración: hay
  // que juntarlos todos o se pega media traducción.
  const leer = () => [...document.querySelectorAll('[jsname="W297wb"]')]
    .map(n => n.textContent).join('').trim();

  // El cuadro es de un framework que escucha los eventos, no la propiedad: hay
  // que escribir el valor por el setter del prototipo y avisar con un evento,
  // como hace el teclado.
  const setter = Object.getOwnPropertyDescriptor(HTMLTextAreaElement.prototype, 'value').set;
  const escribir = (valor) => {
    setter.call(entrada, valor);
    entrada.dispatchEvent(new Event('input', {bubbles: true}));
  };
  const dormir = (ms) => new Promise(r => setTimeout(r, ms));

  // Primero se vacía, y se espera a que el resultado se vacíe con él. Sin esto
  // no hay forma de distinguir la traducción nueva de la que ya estaba en
  // pantalla, y dictar dos veces seguidas pegaba la anterior. Borrarle el texto
  // al nodo a mano no alcanza: la aplicación lo vuelve a poner.
  if (leer() !== '') {
    escribir('');
    const hastaVacio = Date.now() + Math.min(4000, limite / 2);
    while (Date.now() < hastaVacio && leer() !== '') await dormir(60);
    if (leer() !== '') return {error: 'la página de Google se quedó con la traducción anterior'};
  }

  escribir(texto);

  // Google contesta dos veces: primero una traducción rápida y literal, y un
  // rato después la reemplaza por la buena, la que traduce por sentido. Cortar
  // apenas el texto deja de cambiar se lleva la primera —"Tomorrow I'm going to
  // be shit" en vez de "a wreck"— así que hay que esperar a que se quede quieto
  // casi un segundo. Es la diferencia entera entre este camino y el endpoint.
  const fin = Date.now() + limite;
  let ultimo = '', quieto = 0;
  while (Date.now() < fin) {
    await dormir(80);
    const ahora = leer();
    if (ahora && ahora === ultimo) {
      quieto += 80;
      if (quieto >= 880) return {text: ahora};
    } else {
      ultimo = ahora;
      quieto = 0;
    }
  }
  return ultimo ? {text: ultimo} : {error: 'la página de Google no tradujo a tiempo'};
})()`

// guardianScript deja a la página del traductor mirando si el dictador sigue
// vivo, y cerrándose sola cuando deja de estarlo.
//
// Sin esto, un dictador que muere de mala manera —un kill -9, un cuelgue, la
// sesión que se cierra— deja su Chrome dando vueltas para siempre: la página
// del dictado se da cuenta y se cierra, pero el navegador sigue vivo mientras
// le quede una pestaña abierta, y las del traductor no se enteraban de nada.
const guardianScript = `(() => {
  if (window.__dictadorGuardian) return 'ya estaba';
  window.__dictadorGuardian = true;
  const pulso = %s, fallosParaCerrar = %d;
  let fallos = 0;
  setInterval(async () => {
    try {
      await fetch(pulso, {cache: 'no-store'});
      fallos = 0;
    } catch (e) {
      if (++fallos >= fallosParaCerrar) window.close();
    }
  }, 1000);
  return 'listo';
})()`

// webAnswer es lo que devuelve ese script.
type webAnswer struct {
	Text  string `json:"text"`
	Error string `json:"error"`
}

// webTranslator mantiene una pestaña por idioma destino.
//
// Una por idioma y no una sola que cambia de idioma porque cambiarlo obliga a
// recargar la página entera —tres segundos— y alternar entre inglés y
// portugués es justo lo que uno hace.
type webTranslator struct {
	mu sync.Mutex
	// alive es el pulso del dictador, que las pestañas miran para cerrarse
	// solas si se quedan huérfanas.
	alive   string
	port    int
	tabs    map[string]*webTab
	timeout time.Duration
	// ui es el idioma en el que se abre la página de Google.
	ui string
	// usados es el orden en que se usaron los idiomas, del más viejo al más
	// reciente, para saber qué pestaña cerrar cuando sobran.
	usados []string
}

// maxTabs es cuántas páginas del traductor se mantienen abiertas.
//
// Cada una es una aplicación web entera y ocupa lo suyo, así que tener una por
// cada idioma configurado sería caro para el que puso diez. Tres alcanzan para
// que alternar entre los dos o tres de siempre no cueste nada, y el cuarto
// paga la recarga.
const maxTabs = 3

type webTab struct {
	target cdp.Target
	conn   *cdp.Conn
}

// close suelta la pestaña y su conexión.
//
// Tolera una pestaña a medio armar: cerrar es lo último que pasa en varios
// caminos de error, y una pestaña rota no puede tirar abajo el dictado.
func (t *webTab) close(port int) {
	if t == nil {
		return
	}
	if t.conn != nil {
		t.conn.Close()
	}
	if t.target.ID != "" {
		_ = cdp.CloseTab(port, t.target.ID, 2*time.Second)
	}
}

func newWebTranslator(port int, timeout time.Duration, ui, alive string) *webTranslator {
	if timeout <= 0 {
		timeout = 10 * time.Second
	}
	return &webTranslator{
		port: port, tabs: map[string]*webTab{}, timeout: timeout, ui: ui, alive: alive,
	}
}

// guardianRetries es cuántos segundos sin pulso hacen que la pestaña se cierre.
//
// Corto a propósito: la pestaña no sirve para nada sin el dictador, y lo que
// está en juego es un Chrome entero vivo en la máquina de alguien.
var guardianRetries = 8

// Translate escribe el texto en la página y devuelve lo que aparece traducido.
func (w *webTranslator) Translate(text, target string) (string, error) {
	w.mu.Lock()
	defer w.mu.Unlock()

	tab, err := w.tabLocked(target)
	if err != nil {
		return "", err
	}
	w.usarLocked(target)
	// Y al frente de nuevo en cada traducción: entre una y otra pudo pasar
	// cualquier cosa —otra pestaña, la del dictado— y la de atrás no traduce.
	_ = cdp.Activate(w.port, tab.target.ID, w.timeout)
	answer, err := w.ask(tab, text)
	if err != nil || answer.Error != "" {
		// Una pestaña que se murió —Chrome reiniciado, pestaña cerrada— o que
		// no contestó a tiempo se descarta y se intenta una vez más con una
		// nueva. Es el caso normal después de cambiar de motor en caliente, no
		// una rareza; y una pestaña recién abierta que falla la primera vez
		// anda bien la segunda.
		w.dropLocked(target)
		tab, err2 := w.tabLocked(target)
		if err2 != nil {
			if err == nil {
				err = errf("%s", answer.Error)
			}
			return "", err
		}
		answer, err = w.ask(tab, text)
		if err != nil {
			return "", err
		}
	}
	if answer.Error != "" {
		return "", errf("%s", answer.Error)
	}
	if strings.TrimSpace(answer.Text) == "" {
		return "", errf("la página de Google devolvió un texto vacío")
	}
	return answer.Text, nil
}

func (w *webTranslator) ask(tab *webTab, text string) (webAnswer, error) {
	quoted, _ := json.Marshal(text)
	// Al script se le da un poco menos de tiempo que a la conexión, para que
	// sea él el que conteste "no llegué" y no un socket cortado.
	limit := w.timeout - time.Second
	if limit < time.Second {
		limit = time.Second
	}
	script := fmt.Sprintf(translateScript, quoted, limit.Milliseconds())
	raw, err := tab.conn.Evaluate(script, w.timeout)
	if err != nil {
		return webAnswer{}, err
	}
	var answer webAnswer
	if err := json.Unmarshal(raw, &answer); err != nil {
		return webAnswer{}, errf("no entendí lo que devolvió la página: %v", err)
	}
	return answer, nil
}

// Tab devuelve la pestaña de ese idioma, abriéndola si hace falta.
func (w *webTranslator) Tab(target string) (*webTab, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.tabLocked(target)
}

// tabLocked es lo mismo, para los que ya tienen el candado tomado.
//
// El sufijo está para que se vea en el lugar donde se lo llama: todo esto toca
// el mapa de pestañas, y llamarlo sin el candado es una carrera —la encontró el
// detector de carreras, llamándolo desde una prueba—.
func (w *webTranslator) tabLocked(target string) (*webTab, error) {
	if tab, ok := w.tabs[target]; ok {
		return tab, nil
	}
	created, err := cdp.NewTab(w.port, translateURL(target, w.ui), w.timeout)
	if err != nil {
		return nil, errf("no pude abrir la página del traductor: %v", err)
	}
	conn, err := cdp.Dial(created.WS, w.timeout)
	if err != nil {
		_ = cdp.CloseTab(w.port, created.ID, w.timeout)
		return nil, errf("no pude hablar con la página del traductor: %v", err)
	}
	// Al frente, porque una pestaña escondida no traduce.
	_ = cdp.Activate(w.port, created.ID, w.timeout)
	tab := &webTab{target: created, conn: conn}
	if err := w.waitReady(tab); err != nil {
		conn.Close()
		_ = cdp.CloseTab(w.port, created.ID, w.timeout)
		return nil, err
	}
	// El guardián primero: si el dictador se muere mientras esto arranca, la
	// pestaña tiene que poder cerrarse igual.
	if w.alive != "" {
		pulso, _ := json.Marshal(w.alive)
		_, _ = tab.conn.Evaluate(fmt.Sprintf(guardianScript, pulso, guardianRetries), 5*time.Second)
	}
	// Y una traducción de descarte: aunque la página diga estar lista, la
	// primera que se le pide es la que termina de despertarla. Medido: la
	// primera vuelve vacía y de la segunda en adelante sale bien.
	_, _ = w.ask(tab, "hola")
	w.tabs[target] = tab
	w.usarLocked(target)
	w.podarLocked()
	return tab, nil
}

// Warm abre y calienta las pestañas de esos idiomas.
//
// La primera traducción de una pestaña recién abierta es la lenta —la página
// termina de armarse mientras se le escribe— y ese costo no puede caer sobre el
// primer dictado del día. Se paga acá, en segundo plano, apenas Chrome está
// listo.
func (w *webTranslator) Warm(languages []string) {
	for _, language := range languages {
		_, _ = w.Tab(language)
	}
}

// waitReady espera a que la página termine de armarse.
//
// El cuadro de texto aparece bastante antes de que la página esté lista: con el
// documento todavía cargando, lo que se escriba ahí se pierde —la primera
// traducción volvía vacía— así que se esperan las dos cosas.
func (w *webTranslator) waitReady(tab *webTab) error {
	deadline := time.Now().Add(w.timeout)
	for time.Now().Before(deadline) {
		raw, err := tab.conn.Evaluate(
			"document.readyState === 'complete' && !!document.querySelector('textarea')",
			3*time.Second)
		if err == nil {
			var ready bool
			if json.Unmarshal(raw, &ready) == nil && ready {
				return nil
			}
		}
		time.Sleep(150 * time.Millisecond)
	}
	return errf("la página del traductor no terminó de cargar")
}

// usarLocked anota que este idioma es el más reciente. Con el candado tomado.
func (w *webTranslator) usarLocked(target string) {
	for i, t := range w.usados {
		if t == target {
			w.usados = append(w.usados[:i], w.usados[i+1:]...)
			break
		}
	}
	w.usados = append(w.usados, target)
}

// podarLocked cierra las páginas que sobran, empezando por la que hace más que
// no se usa. Con el candado tomado.
func (w *webTranslator) podarLocked() {
	for len(w.tabs) > maxTabs && len(w.usados) > 0 {
		viejo := w.usados[0]
		w.usados = w.usados[1:]
		if _, ok := w.tabs[viejo]; ok {
			w.dropLocked(viejo)
		}
	}
}

// dropLocked descarta la pestaña de un idioma. Con el candado tomado.
func (w *webTranslator) dropLocked(target string) {
	tab, ok := w.tabs[target]
	if !ok {
		return
	}
	delete(w.tabs, target)
	for i, t := range w.usados {
		if t == target {
			w.usados = append(w.usados[:i], w.usados[i+1:]...)
			break
		}
	}
	tab.close(w.port)
}

// Close cierra las pestañas que abrimos.
func (w *webTranslator) Close() {
	w.mu.Lock()
	defer w.mu.Unlock()
	for target := range w.tabs {
		w.tabs[target].close(w.port)
		delete(w.tabs, target)
	}
	w.usados = nil
}
