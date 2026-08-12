package audio

import (
	"encoding/binary"
	"math"
	"testing"
)

func pcmFrom(samples ...int16) []byte {
	out := make([]byte, len(samples)*2)
	for i, s := range samples {
		binary.LittleEndian.PutUint16(out[2*i:], uint16(s))
	}
	return out
}

func TestDecodeVuelveALosMismosValores(t *testing.T) {
	got := decode(pcmFrom(0, 32767, -32768, 16384))
	want := []float32{0, 32767.0 / 32768.0, -1, 0.5}
	if len(got) != len(want) {
		t.Fatalf("largo %d, quería %d", len(got), len(want))
	}
	for i := range want {
		if math.Abs(float64(got[i]-want[i])) > 1e-6 {
			t.Errorf("muestra %d = %v, quería %v", i, got[i], want[i])
		}
	}
}

func TestDecodeIgnoraElByteImpar(t *testing.T) {
	// parec puede cortar en la mitad de una muestra cuando lo matamos.
	if got := decode([]byte{0x00, 0x40, 0x11}); len(got) != 1 {
		t.Errorf("debería quedarse con la muestra entera y tirar el sobrante, dio %v", got)
	}
}

func TestRMSDeSilencioYDeTono(t *testing.T) {
	if got := rms(pcmFrom(0, 0, 0, 0)); got != 0 {
		t.Errorf("el silencio tendría que dar 0, dio %v", got)
	}
	// Una onda cuadrada a fondo de escala tiene RMS 1.
	if got := rms(pcmFrom(32767, -32767, 32767, -32767)); math.Abs(got-1) > 0.001 {
		t.Errorf("RMS = %v, quería ~1", got)
	}
}

func TestToPCM16RecortaFueraDeRango(t *testing.T) {
	got := ToPCM16([]float32{2, -2})
	if int16(binary.LittleEndian.Uint16(got[0:])) != 32767 {
		t.Errorf("no recortó el positivo: %v", got[:2])
	}
	if int16(binary.LittleEndian.Uint16(got[2:])) != -32767 {
		t.Errorf("no recortó el negativo: %v", got[2:])
	}
}

func TestWAVTieneElHeaderQueDice(t *testing.T) {
	pcm := pcmFrom(1, 2, 3, 4)
	w := WAV(pcm, 16000)
	if string(w[0:4]) != "RIFF" || string(w[8:12]) != "WAVE" {
		t.Fatalf("no es un RIFF/WAVE: %q", w[:12])
	}
	if got := binary.LittleEndian.Uint32(w[4:]); got != uint32(36+len(pcm)) {
		t.Errorf("tamaño RIFF = %d", got)
	}
	if got := binary.LittleEndian.Uint32(w[24:]); got != 16000 {
		t.Errorf("sample rate = %d", got)
	}
	if got := binary.LittleEndian.Uint32(w[40:]); got != uint32(len(pcm)) {
		t.Errorf("tamaño del chunk data = %d", got)
	}
	if len(w) != 44+len(pcm) {
		t.Errorf("largo total = %d, quería %d", len(w), 44+len(pcm))
	}
}
