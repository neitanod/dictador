package audio

import (
	"bytes"
	"encoding/binary"
	"math"
)

// ToPCM16 convierte float32 en [-1, 1] a s16le, que es lo que entienden tanto
// Google como whisper-server.
func ToPCM16(samples []float32) []byte {
	out := make([]byte, len(samples)*2)
	for i, s := range samples {
		v := float64(s)
		if v > 1 {
			v = 1
		} else if v < -1 {
			v = -1
		}
		binary.LittleEndian.PutUint16(out[2*i:], uint16(int16(math.Round(v*32767))))
	}
	return out
}

// WAV envuelve el PCM en un RIFF mono de 16 bits.
func WAV(pcm []byte, sampleRate int) []byte {
	var buf bytes.Buffer
	write := func(v any) { _ = binary.Write(&buf, binary.LittleEndian, v) }

	buf.WriteString("RIFF")
	write(uint32(36 + len(pcm)))
	buf.WriteString("WAVEfmt ")
	write(uint32(16))             // tamaño del bloque fmt
	write(uint16(1))              // PCM sin comprimir
	write(uint16(1))              // mono
	write(uint32(sampleRate))     //
	write(uint32(sampleRate * 2)) // bytes por segundo
	write(uint16(2))              // alineación de bloque
	write(uint16(16))             // bits por muestra
	buf.WriteString("data")
	write(uint32(len(pcm)))
	buf.Write(pcm)
	return buf.Bytes()
}
