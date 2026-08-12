package x11

import (
	"bytes"
	"encoding/binary"
	"io"
	"net"
	"testing"
	"time"
)

// pipeConn es un net.Conn de mentira que sólo sabe leer de un buffer: alcanza
// para probar el reencuadre, que es lo único que hace el filtro.
type pipeConn struct {
	net.Conn
	r io.Reader
}

func (p *pipeConn) Read(b []byte) (int, error) { return p.r.Read(b) }

func message(kind byte, extraWords uint32, payload []byte) []byte {
	msg := make([]byte, 32)
	msg[0] = kind
	binary.LittleEndian.PutUint32(msg[4:], extraWords)
	return append(msg, payload...)
}

// TestFiltroTiraElPayloadDeLosGenericEvent es el test del riesgo que motivó el
// filtro: si el payload extra de un GenericEvent se quedara en el socket, todo
// lo que viene después se leería corrido y la conexión quedaría basura.
func TestFiltroTiraElPayloadDeLosGenericEvent(t *testing.T) {
	// Un GenericEvent con 2 palabras de payload, y detrás un evento común.
	ge := message(xgeEventType, 2, bytes.Repeat([]byte{0xAA}, 8))
	plain := message(2, 0, nil) // un KeyPress cualquiera
	plain[1] = 0x42             // una marca para reconocerlo

	filter := &genericEventFilter{Conn: &pipeConn{r: bytes.NewReader(append(ge, plain...))}}
	filter.active.Store(true)

	first := make([]byte, 32)
	if _, err := io.ReadFull(filter, first); err != nil {
		t.Fatal(err)
	}
	if first[0] != xgeEventType {
		t.Fatalf("el primer mensaje tendría que ser el GenericEvent: %d", first[0])
	}

	second := make([]byte, 32)
	if _, err := io.ReadFull(filter, second); err != nil {
		t.Fatal(err)
	}
	if second[0] != 2 || second[1] != 0x42 {
		t.Errorf("el evento de atrás llegó corrido: %v", second[:4])
	}
}

func TestFiltroDejaPasarLasRespuestasEnteras(t *testing.T) {
	// Una respuesta con 3 palabras de datos: xgb lee 32 bytes y después pide
	// los 12 restantes, y el filtro se los tiene que servir en ese orden.
	payload := []byte("doce bytes..")
	reply := message(1, 3, payload)

	filter := &genericEventFilter{Conn: &pipeConn{r: bytes.NewReader(reply)}}
	filter.active.Store(true)

	head := make([]byte, 32)
	if _, err := io.ReadFull(filter, head); err != nil {
		t.Fatal(err)
	}
	if head[0] != 1 {
		t.Fatalf("no es una respuesta: %d", head[0])
	}
	tail := make([]byte, 12)
	if _, err := io.ReadFull(filter, tail); err != nil {
		t.Fatal(err)
	}
	if string(tail) != string(payload) {
		t.Errorf("el cuerpo de la respuesta llegó mal: %q", tail)
	}
}

func TestFiltroApagadoNoTocaNada(t *testing.T) {
	// Durante el saludo inicial el filtro tiene que ser transparente: ahí las
	// lecturas no son de 32 bytes y reencuadrarlas rompería la conexión.
	raw := []byte("hola")
	filter := &genericEventFilter{Conn: &pipeConn{r: bytes.NewReader(raw)}}

	buf := make([]byte, 4)
	if _, err := io.ReadFull(filter, buf); err != nil {
		t.Fatal(err)
	}
	if string(buf) != "hola" {
		t.Errorf("leyó %q", buf)
	}
}

func TestFiltroDevuelveElErrorDeLectura(t *testing.T) {
	filter := &genericEventFilter{Conn: &pipeConn{r: bytes.NewReader(nil)}}
	filter.active.Store(true)
	if _, err := filter.Read(make([]byte, 32)); err == nil {
		t.Error("con el socket cerrado tiene que devolver error")
	}
}

// TestFiltroNoSeCuelga es una red de seguridad: el reencuadre corre adentro del
// read loop de xgb, así que una espera infinita ahí congelaría la app entera.
func TestFiltroNoSeCuelga(t *testing.T) {
	done := make(chan struct{})
	go func() {
		defer close(done)
		filter := &genericEventFilter{
			Conn: &pipeConn{r: bytes.NewReader(message(xgeEventType, 0, nil))},
		}
		filter.active.Store(true)
		_, _ = filter.Read(make([]byte, 32))
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("el filtro se colgó")
	}
}
