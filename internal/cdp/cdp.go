// Package cdp le habla al Chrome que ya tenemos vivo por el protocolo de sus
// DevTools.
//
// Existe por una sola razón: la traducción buena de Google sale de la interfaz
// de translate.google.com y no de ninguna API. El endpoint público devuelve el
// modelo viejo —"Tomorrow I'm going to be shit"— y lo sigue devolviendo aunque
// se lo llame desde la propia página de Google con las cookies puestas: lo que
// habilita el modelo nuevo es un token antifraude que el JavaScript de Google
// arma para sus propios pedidos y que desde afuera no se puede fabricar. Así
// que se escribe en el cuadro de texto de la página y se lee lo que aparece del
// otro lado, que es exactamente lo que hace una persona.
//
// El protocolo es JSON-RPC sobre un WebSocket, y el WebSocket está escrito acá
// abajo a mano. Es la misma decisión que el resto de la app: X11 se habla
// directo en vez de llamar a xdotool, y esto son ciento y pico de líneas contra
// una dependencia entera de la que usaríamos tres funciones.
package cdp

import (
	"bufio"
	"crypto/rand"
	"crypto/sha1"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

// Target es una pestaña del navegador.
type Target struct {
	ID    string `json:"id"`
	Type  string `json:"type"`
	URL   string `json:"url"`
	Title string `json:"title"`
	WS    string `json:"webSocketDebuggerUrl"`
}

// Targets lista lo que el navegador tiene abierto.
func Targets(port int, timeout time.Duration) ([]Target, error) {
	client := &http.Client{Timeout: timeout}
	res, err := client.Get(fmt.Sprintf("http://127.0.0.1:%d/json/list", port))
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()
	var targets []Target
	if err := json.NewDecoder(res.Body).Decode(&targets); err != nil {
		return nil, err
	}
	return targets, nil
}

// NewTab abre una pestaña nueva y la devuelve.
func NewTab(port int, address string, timeout time.Duration) (Target, error) {
	client := &http.Client{Timeout: timeout}
	res, err := client.Post(fmt.Sprintf("http://127.0.0.1:%d/json/new?%s",
		port, url.QueryEscape(address)), "", nil)
	if err != nil {
		return Target{}, err
	}
	defer res.Body.Close()
	// Las versiones nuevas de Chrome piden PUT y rechazan el POST con un 405,
	// diciéndolo en el cuerpo. Se reintenta con PUT en vez de exigir una
	// versión mínima.
	if res.StatusCode == http.StatusMethodNotAllowed {
		req, _ := http.NewRequest(http.MethodPut,
			fmt.Sprintf("http://127.0.0.1:%d/json/new?%s", port, url.QueryEscape(address)), nil)
		res2, err := client.Do(req)
		if err != nil {
			return Target{}, err
		}
		defer res2.Body.Close()
		return decodeTarget(res2)
	}
	return decodeTarget(res)
}

func decodeTarget(res *http.Response) (Target, error) {
	if res.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(res.Body, 200))
		return Target{}, fmt.Errorf("el navegador contestó %d: %s", res.StatusCode, strings.TrimSpace(string(body)))
	}
	var target Target
	if err := json.NewDecoder(res.Body).Decode(&target); err != nil {
		return Target{}, err
	}
	return target, nil
}

// Activate trae una pestaña al frente.
//
// Hace falta porque Google no traduce en una pestaña que el navegador considera
// escondida: la página escucha su propia visibilidad.
func Activate(port int, id string, timeout time.Duration) error {
	client := &http.Client{Timeout: timeout}
	res, err := client.Get(fmt.Sprintf("http://127.0.0.1:%d/json/activate/%s", port, id))
	if err != nil {
		return err
	}
	defer res.Body.Close()
	return nil
}

// CloseTab cierra una pestaña por id.
func CloseTab(port int, id string, timeout time.Duration) error {
	client := &http.Client{Timeout: timeout}
	res, err := client.Get(fmt.Sprintf("http://127.0.0.1:%d/json/close/%s", port, id))
	if err != nil {
		return err
	}
	defer res.Body.Close()
	return nil
}

// Conn es una conexión abierta contra una pestaña.
type Conn struct {
	mu   sync.Mutex
	conn net.Conn
	buf  *bufio.Reader
	next int
}

// Dial se conecta al WebSocket de una pestaña.
func Dial(wsURL string, timeout time.Duration) (*Conn, error) {
	u, err := url.Parse(wsURL)
	if err != nil {
		return nil, err
	}
	host := u.Host
	if !strings.Contains(host, ":") {
		host += ":80"
	}
	conn, err := net.DialTimeout("tcp", host, timeout)
	if err != nil {
		return nil, err
	}
	key := make([]byte, 16)
	if _, err := rand.Read(key); err != nil {
		conn.Close()
		return nil, err
	}
	encoded := base64.StdEncoding.EncodeToString(key)
	path := u.RequestURI()
	handshake := "GET " + path + " HTTP/1.1\r\n" +
		"Host: " + u.Host + "\r\n" +
		"Upgrade: websocket\r\n" +
		"Connection: Upgrade\r\n" +
		"Sec-WebSocket-Key: " + encoded + "\r\n" +
		"Sec-WebSocket-Version: 13\r\n\r\n"
	if err := conn.SetDeadline(time.Now().Add(timeout)); err != nil {
		conn.Close()
		return nil, err
	}
	if _, err := io.WriteString(conn, handshake); err != nil {
		conn.Close()
		return nil, err
	}
	buf := bufio.NewReaderSize(conn, 64*1024)
	res, err := http.ReadResponse(buf, &http.Request{Method: "GET"})
	if err != nil {
		conn.Close()
		return nil, err
	}
	res.Body.Close()
	if res.StatusCode != http.StatusSwitchingProtocols {
		conn.Close()
		return nil, fmt.Errorf("el navegador no aceptó el WebSocket: %s", res.Status)
	}
	// La respuesta trae la clave que mandamos, hasheada con la constante del
	// protocolo. Verificarlo es barato y descarta que del otro lado haya
	// cualquier otra cosa escuchando en ese puerto.
	sum := sha1.Sum([]byte(encoded + "258EAFA5-E914-47DA-95CA-C5AB0DC85B11"))
	if res.Header.Get("Sec-WebSocket-Accept") != base64.StdEncoding.EncodeToString(sum[:]) {
		conn.Close()
		return nil, fmt.Errorf("el WebSocket contestó con una clave que no es la nuestra")
	}
	_ = conn.SetDeadline(time.Time{})
	return &Conn{conn: conn, buf: buf}, nil
}

// Close corta la conexión.
func (c *Conn) Close() {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.conn != nil {
		_ = c.conn.Close()
		c.conn = nil
	}
}

// call manda un método y espera su respuesta.
//
// Las respuestas vienen con el id del pedido y los eventos vienen sin id: se
// leen y se tiran hasta que aparece la que corresponde. No hay concurrencia
// acá —el daemon traduce de a una— así que un candado alrededor de todo el
// viaje alcanza y sobra.
func (c *Conn) call(method string, params any, timeout time.Duration) (json.RawMessage, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.conn == nil {
		return nil, fmt.Errorf("la conexión con el navegador está cerrada")
	}
	c.next++
	id := c.next
	payload, err := json.Marshal(map[string]any{"id": id, "method": method, "params": params})
	if err != nil {
		return nil, err
	}
	deadline := time.Now().Add(timeout)
	if err := c.conn.SetDeadline(deadline); err != nil {
		return nil, err
	}
	if err := writeFrame(c.conn, payload); err != nil {
		return nil, err
	}
	for {
		frame, err := c.readFrame()
		if err != nil {
			return nil, err
		}
		var answer struct {
			ID     int             `json:"id"`
			Result json.RawMessage `json:"result"`
			Error  *struct {
				Message string `json:"message"`
			} `json:"error"`
		}
		if err := json.Unmarshal(frame, &answer); err != nil {
			continue
		}
		if answer.ID != id {
			continue // un evento, o la respuesta de otro pedido
		}
		if answer.Error != nil {
			return nil, fmt.Errorf("%s: %s", method, answer.Error.Message)
		}
		return answer.Result, nil
	}
}

// Evaluate corre JavaScript en la pestaña y devuelve el valor.
//
// Espera las promesas del otro lado a propósito: la traducción se pide y se
// recibe adentro del navegador, en un solo viaje, en vez de preguntar cada
// cien milisegundos desde acá.
func (c *Conn) Evaluate(expression string, timeout time.Duration) (json.RawMessage, error) {
	raw, err := c.call("Runtime.evaluate", map[string]any{
		"expression":    expression,
		"returnByValue": true,
		"awaitPromise":  true,
	}, timeout)
	if err != nil {
		return nil, err
	}
	var answer struct {
		Result struct {
			Value json.RawMessage `json:"value"`
		} `json:"result"`
		Exception *struct {
			Text     string `json:"text"`
			Details  string `json:"description"`
			Excepted struct {
				Description string `json:"description"`
			} `json:"exception"`
		} `json:"exceptionDetails"`
	}
	if err := json.Unmarshal(raw, &answer); err != nil {
		return nil, err
	}
	if answer.Exception != nil {
		message := answer.Exception.Excepted.Description
		if message == "" {
			message = answer.Exception.Text
		}
		return nil, fmt.Errorf("la página falló: %s", message)
	}
	return answer.Result.Value, nil
}

// Navigate manda la pestaña a otra dirección.
func (c *Conn) Navigate(address string, timeout time.Duration) error {
	_, err := c.call("Page.navigate", map[string]any{"url": address}, timeout)
	return err
}

// ---- el WebSocket, a mano ------------------------------------------------
//
// Sólo hace falta el pedacito del protocolo que usa DevTools: marcos de texto,
// del cliente al servidor enmascarados, del servidor al cliente sin máscara, y
// el ping que hay que contestar para que la conexión no se caiga sola.

const (
	opText  = 0x1
	opClose = 0x8
	opPing  = 0x9
	opPong  = 0xA
)

func writeFrame(w io.Writer, payload []byte) error {
	return writeFrameOp(w, opText, payload)
}

func writeFrameOp(w io.Writer, opcode byte, payload []byte) error {
	header := []byte{0x80 | opcode} // FIN + opcode
	length := len(payload)
	switch {
	case length < 126:
		header = append(header, byte(0x80|length)) // el bit de máscara va siempre
	case length < 1<<16:
		header = append(header, 0x80|126, 0, 0)
		binary.BigEndian.PutUint16(header[2:], uint16(length))
	default:
		header = append(header, 0x80|127)
		header = append(header, make([]byte, 8)...)
		binary.BigEndian.PutUint64(header[2:], uint64(length))
	}
	mask := make([]byte, 4)
	if _, err := rand.Read(mask); err != nil {
		return err
	}
	header = append(header, mask...)
	masked := make([]byte, length)
	for i := 0; i < length; i++ {
		masked[i] = payload[i] ^ mask[i%4]
	}
	if _, err := w.Write(header); err != nil {
		return err
	}
	_, err := w.Write(masked)
	return err
}

// readFrame devuelve el próximo marco de texto, juntando los que vengan
// partidos y contestando los pings por el camino.
func (c *Conn) readFrame() ([]byte, error) {
	r := c.buf
	var message []byte
	for {
		first, err := r.ReadByte()
		if err != nil {
			return nil, err
		}
		fin := first&0x80 != 0
		opcode := first & 0x0f
		second, err := r.ReadByte()
		if err != nil {
			return nil, err
		}
		masked := second&0x80 != 0
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
		if masked {
			if _, err := io.ReadFull(r, mask[:]); err != nil {
				return nil, err
			}
		}
		payload := make([]byte, length)
		if _, err := io.ReadFull(r, payload); err != nil {
			return nil, err
		}
		if masked {
			for i := range payload {
				payload[i] ^= mask[i%4]
			}
		}
		switch opcode {
		case opClose:
			return nil, fmt.Errorf("el navegador cerró la conexión")
		case opPing:
			// Contestar el ping es lo único que mantiene viva una conexión que
			// se queda quieta entre dictado y dictado.
			if err := writeFrameOp(c.conn, opPong, payload); err != nil {
				return nil, err
			}
			continue
		case opPong:
			continue
		}
		message = append(message, payload...)
		if fin {
			return message, nil
		}
	}
}
