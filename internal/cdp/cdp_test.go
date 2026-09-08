package cdp

import (
	"bufio"
	"crypto/sha1"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"
)

// servidorFalso habla el pedacito de WebSocket que necesitamos, del otro lado.
//
// El protocolo está escrito a mano en este paquete, así que hace falta alguien
// que lo lea igual de a mano: un servidor de verdad contestaría lo mismo, y
// éste además puede mandar los casos raros —el marco partido, el ping— que un
// Chrome sano no manda nunca.
func servidorFalso(t *testing.T, responder func(pedido map[string]any, w *bufio.Writer, conn net.Conn)) (string, func()) {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("no pude abrir el server de prueba: %v", err)
	}
	go func() {
		conn, err := listener.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		buf := bufio.NewReader(conn)
		req, err := http.ReadRequest(buf)
		if err != nil {
			return
		}
		sum := sha1.Sum([]byte(req.Header.Get("Sec-WebSocket-Key") + "258EAFA5-E914-47DA-95CA-C5AB0DC85B11"))
		_, _ = io.WriteString(conn, "HTTP/1.1 101 Switching Protocols\r\n"+
			"Upgrade: websocket\r\nConnection: Upgrade\r\n"+
			"Sec-WebSocket-Accept: "+base64.StdEncoding.EncodeToString(sum[:])+"\r\n\r\n")
		w := bufio.NewWriter(conn)
		for {
			frame, err := leerMarco(buf)
			if err != nil {
				return
			}
			var pedido map[string]any
			if json.Unmarshal(frame, &pedido) != nil {
				return
			}
			responder(pedido, w, conn)
		}
	}()
	address := "ws://" + listener.Addr().String() + "/devtools/page/falsa"
	return address, func() { _ = listener.Close() }
}

// leerMarco lee un marco enmascarado, que es como manda el cliente.
func leerMarco(r *bufio.Reader) ([]byte, error) {
	first, err := r.ReadByte()
	if err != nil {
		return nil, err
	}
	_ = first
	second, err := r.ReadByte()
	if err != nil {
		return nil, err
	}
	length := int(second & 0x7f)
	switch length {
	case 126:
		var extended uint16
		if err := binary.Read(r, binary.BigEndian, &extended); err != nil {
			return nil, err
		}
		length = int(extended)
	case 127:
		var extended uint64
		if err := binary.Read(r, binary.BigEndian, &extended); err != nil {
			return nil, err
		}
		length = int(extended)
	}
	var mask [4]byte
	if second&0x80 != 0 {
		if _, err := io.ReadFull(r, mask[:]); err != nil {
			return nil, err
		}
	}
	payload := make([]byte, length)
	if _, err := io.ReadFull(r, payload); err != nil {
		return nil, err
	}
	if second&0x80 != 0 {
		for i := range payload {
			payload[i] ^= mask[i%4]
		}
	}
	return payload, nil
}

// escribirMarco manda un marco sin máscara, que es como contesta el servidor.
func escribirMarco(w *bufio.Writer, opcode byte, payload []byte, fin bool) {
	first := opcode
	if fin {
		first |= 0x80
	}
	_ = w.WriteByte(first)
	switch {
	case len(payload) < 126:
		_ = w.WriteByte(byte(len(payload)))
	default:
		_ = w.WriteByte(126)
		var extended [2]byte
		binary.BigEndian.PutUint16(extended[:], uint16(len(payload)))
		_, _ = w.Write(extended[:])
	}
	_, _ = w.Write(payload)
	_ = w.Flush()
}

func responderValor(valor string) func(map[string]any, *bufio.Writer, net.Conn) {
	return func(pedido map[string]any, w *bufio.Writer, _ net.Conn) {
		answer := map[string]any{
			"id": pedido["id"],
			"result": map[string]any{
				"result": map[string]any{"value": json.RawMessage(valor)},
			},
		}
		raw, _ := json.Marshal(answer)
		escribirMarco(w, opText, raw, true)
	}
}

func TestEvaluateDevuelveElValor(t *testing.T) {
	address, cerrar := servidorFalso(t, responderValor(`"hola"`))
	defer cerrar()

	conn, err := Dial(address, 2*time.Second)
	if err != nil {
		t.Fatalf("no pude conectarme: %v", err)
	}
	defer conn.Close()

	raw, err := conn.Evaluate("1+1", 2*time.Second)
	if err != nil {
		t.Fatalf("falló la evaluación: %v", err)
	}
	if string(raw) != `"hola"` {
		t.Errorf("devolvió %s", raw)
	}
}

// Los eventos llegan mezclados con las respuestas y sin id: hay que saltearlos
// en vez de tomarlos por la contestación de lo que preguntamos.
func TestLosEventosNoSeConfundenConLaRespuesta(t *testing.T) {
	address, cerrar := servidorFalso(t, func(pedido map[string]any, w *bufio.Writer, _ net.Conn) {
		evento, _ := json.Marshal(map[string]any{
			"method": "Page.frameNavigated", "params": map[string]any{},
		})
		escribirMarco(w, opText, evento, true)
		responderValor(`42`)(pedido, w, nil)
	})
	defer cerrar()

	conn, err := Dial(address, 2*time.Second)
	if err != nil {
		t.Fatalf("no pude conectarme: %v", err)
	}
	defer conn.Close()
	raw, err := conn.Evaluate("40+2", 2*time.Second)
	if err != nil || string(raw) != "42" {
		t.Fatalf("devolvió %s (err %v)", raw, err)
	}
}

// Una respuesta larga viene partida en varios marcos, y hay que juntarla.
func TestUnaRespuestaPartidaSeJunta(t *testing.T) {
	address, cerrar := servidorFalso(t, func(pedido map[string]any, w *bufio.Writer, _ net.Conn) {
		raw, _ := json.Marshal(map[string]any{
			"id": pedido["id"],
			"result": map[string]any{
				"result": map[string]any{"value": strings.Repeat("a", 300)},
			},
		})
		mitad := len(raw) / 2
		escribirMarco(w, opText, raw[:mitad], false)
		escribirMarco(w, 0x0, raw[mitad:], true) // continuación
	})
	defer cerrar()

	conn, err := Dial(address, 2*time.Second)
	if err != nil {
		t.Fatalf("no pude conectarme: %v", err)
	}
	defer conn.Close()
	got, err := conn.Evaluate("largo", 2*time.Second)
	if err != nil {
		t.Fatalf("falló: %v", err)
	}
	var texto string
	if json.Unmarshal(got, &texto) != nil || len(texto) != 300 {
		t.Errorf("llegó %d caracteres", len(texto))
	}
}

// Un ping en el medio se contesta y no interrumpe la respuesta que se espera.
func TestElPingSeContesta(t *testing.T) {
	address, cerrar := servidorFalso(t, func(pedido map[string]any, w *bufio.Writer, _ net.Conn) {
		escribirMarco(w, opPing, []byte("ping"), true)
		responderValor(`"listo"`)(pedido, w, nil)
	})
	defer cerrar()

	conn, err := Dial(address, 2*time.Second)
	if err != nil {
		t.Fatalf("no pude conectarme: %v", err)
	}
	defer conn.Close()
	raw, err := conn.Evaluate("1", 2*time.Second)
	if err != nil || string(raw) != `"listo"` {
		t.Fatalf("devolvió %s (err %v)", raw, err)
	}
}

// Un error del otro lado tiene que llegar como error y no como valor vacío.
func TestElErrorDelNavegadorLlegaComoError(t *testing.T) {
	address, cerrar := servidorFalso(t, func(pedido map[string]any, w *bufio.Writer, _ net.Conn) {
		raw, _ := json.Marshal(map[string]any{
			"id":    pedido["id"],
			"error": map[string]any{"message": "Cannot find context with specified id"},
		})
		escribirMarco(w, opText, raw, true)
	})
	defer cerrar()

	conn, err := Dial(address, 2*time.Second)
	if err != nil {
		t.Fatalf("no pude conectarme: %v", err)
	}
	defer conn.Close()
	if _, err := conn.Evaluate("1", 2*time.Second); err == nil {
		t.Fatal("el error del navegador pasó como si nada")
	}
}

// Una excepción de la página tampoco puede pasar por buena: el script que se
// rompe devuelve un resultado vacío que se leería como "no tradujo nada".
func TestLaExcepcionDeLaPaginaLlegaComoError(t *testing.T) {
	address, cerrar := servidorFalso(t, func(pedido map[string]any, w *bufio.Writer, _ net.Conn) {
		raw, _ := json.Marshal(map[string]any{
			"id": pedido["id"],
			"result": map[string]any{
				"result": map[string]any{},
				"exceptionDetails": map[string]any{
					"text":      "Uncaught",
					"exception": map[string]any{"description": "TypeError: ta is null"},
				},
			},
		})
		escribirMarco(w, opText, raw, true)
	})
	defer cerrar()

	conn, err := Dial(address, 2*time.Second)
	if err != nil {
		t.Fatalf("no pude conectarme: %v", err)
	}
	defer conn.Close()
	_, err = conn.Evaluate("boom", 2*time.Second)
	if err == nil || !strings.Contains(err.Error(), "TypeError") {
		t.Fatalf("el error quedó %v", err)
	}
}

// El handshake se verifica: si del otro lado hay cualquier otra cosa escuchando
// en ese puerto, mejor enterarse al conectar.
func TestUnHandshakeQueNoEsElNuestroSeRechaza(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	go func() {
		conn, err := listener.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		buf := bufio.NewReader(conn)
		_, _ = http.ReadRequest(buf)
		_, _ = io.WriteString(conn, "HTTP/1.1 101 Switching Protocols\r\n"+
			"Upgrade: websocket\r\nConnection: Upgrade\r\n"+
			"Sec-WebSocket-Accept: cualquiera\r\n\r\n")
	}()
	if _, err := Dial("ws://"+listener.Addr().String()+"/x", time.Second); err == nil {
		t.Fatal("aceptó una clave que no era la nuestra")
	}
}
