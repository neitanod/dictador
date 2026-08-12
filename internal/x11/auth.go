package x11

import (
	"encoding/binary"
	"encoding/hex"
	"io"
	"os"
	"path/filepath"
)

// La cookie de autenticación se lee acá y no la deja leer xgb porque a
// NewConnNet —la puerta que usamos para poder envolver el socket— nunca se le
// dice qué display es, y entonces busca una entrada para el display "" y no
// encuentra ninguna. Sin esto, cada arranque escupe un "Could not get authority
// info" y se conecta sin credenciales, que funciona hasta que el servidor las
// exige de verdad.

// familyLocal y familyWild son las dos familias que valen para una conexión
// local: la del socket unix y el comodín.
const (
	familyLocal = 256
	familyWild  = 65535
)

// authCookie busca la MIT-MAGIC-COOKIE-1 del display, en hexadecimal.
// Devuelve "" si no hay archivo de autoridad o si no hay entrada para este
// display, que es un caso legítimo (servidores sin autenticación).
func authCookie(displayNum string) string {
	path := os.Getenv("XAUTHORITY")
	if path == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return ""
		}
		path = filepath.Join(home, ".Xauthority")
	}
	file, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer file.Close()

	hostname, _ := os.Hostname()
	for {
		family, err := readCard16(file)
		if err != nil {
			return ""
		}
		address, err := readBlock(file)
		if err != nil {
			return ""
		}
		number, err := readBlock(file)
		if err != nil {
			return ""
		}
		name, err := readBlock(file)
		if err != nil {
			return ""
		}
		data, err := readBlock(file)
		if err != nil {
			return ""
		}

		if string(name) != "MIT-MAGIC-COOKIE-1" {
			continue
		}
		if family != familyLocal && family != familyWild {
			continue
		}
		if string(address) != hostname && string(address) != "localhost" && len(address) != 0 {
			continue
		}
		if string(number) != displayNum && len(number) != 0 {
			continue
		}
		return hex.EncodeToString(data)
	}
}

func readCard16(r io.Reader) (uint16, error) {
	var buf [2]byte
	if _, err := io.ReadFull(r, buf[:]); err != nil {
		return 0, err
	}
	// El archivo de autoridad va en big-endian, a diferencia del protocolo.
	return binary.BigEndian.Uint16(buf[:]), nil
}

func readBlock(r io.Reader) ([]byte, error) {
	size, err := readCard16(r)
	if err != nil {
		return nil, err
	}
	buf := make([]byte, size)
	if _, err := io.ReadFull(r, buf); err != nil {
		return nil, err
	}
	return buf, nil
}
