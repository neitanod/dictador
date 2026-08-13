package commands

import "testing"

func find(t *testing.T, entries []Entry, key string) Entry {
	t.Helper()
	for _, e := range entries {
		if e.Key == key {
			return e
		}
	}
	t.Fatalf("no encontré %q en las %d filas", key, len(entries))
	return Entry{}
}

func TestEntriesTraeElCatalogoEntero(t *testing.T) {
	entries := Entries(Options{Enabled: true})
	if len(entries) != len(catalog) {
		t.Fatalf("esperaba las %d de fábrica, vinieron %d", len(catalog), len(entries))
	}
	coma := find(t, entries, "coma")
	if coma.Writes != "," || coma.Default != "," {
		t.Fatalf("la coma tiene que escribir una coma: %+v", coma)
	}
	if coma.Changed || !coma.Builtin || coma.Fixed {
		t.Fatalf("la coma es de fábrica, editable y sin tocar: %+v", coma)
	}

	// Ordenadas: la ventana las muestra en el orden en que vienen.
	for i := 1; i < len(entries); i++ {
		if entries[i-1].Key > entries[i].Key {
			t.Fatalf("vinieron desordenadas: %q antes que %q", entries[i-1].Key, entries[i].Key)
		}
	}
}

// Los que mandan teclas o borran no se pueden escribir en el config, y la
// ventana necesita saberlo para no ofrecer un campo de texto que no serviría.
func TestEntriesMarcaLosQueNoSonTexto(t *testing.T) {
	entries := Entries(Options{Enabled: true})
	for _, key := range []string{"enter", "tab", "entre corchetes", "borra eso", "punto y aparte"} {
		if e := find(t, entries, key); !e.Fixed {
			t.Fatalf("%q hace algo más que escribir texto y vino como editable: %+v", key, e)
		}
	}
	if e := find(t, entries, "barra"); e.Fixed {
		t.Fatalf("la barra escribe texto y nada más: %+v", e)
	}
}

func TestEntriesMezclaLosTuyos(t *testing.T) {
	entries := Entries(Options{Enabled: true, Replacements: map[string]string{
		"dos puntos": ":",
		"coma":       " —",
		"barra":      "",
	}})
	if len(entries) != len(catalog)+1 {
		t.Fatalf("esperaba una fila más que el catálogo, vinieron %d", len(entries))
	}

	nuevo := find(t, entries, "dos puntos")
	if nuevo.Builtin || !nuevo.Changed || nuevo.Writes != ":" {
		t.Fatalf("el agregado tuyo tiene que venir como tuyo: %+v", nuevo)
	}

	// El pisado muestra lo tuyo y se acuerda de lo que escribía antes, que es
	// lo único que permite volver atrás desde la ventana.
	pisado := find(t, entries, "coma")
	if !pisado.Builtin || !pisado.Changed || pisado.Writes != " —" || pisado.Default != "," {
		t.Fatalf("la coma pisada quedó mal: %+v", pisado)
	}

	apagado := find(t, entries, "barra")
	if !apagado.Off || !apagado.Changed || apagado.Default != "/" {
		t.Fatalf("la barra apagada quedó mal: %+v", apagado)
	}
}

// Las maneras largas de pedir el punto y la coma vienen de fábrica: son las
// que uno dice cuando quiere el signo y no la palabra.
func TestPuntoFinalYPalabraComaSonDeFabrica(t *testing.T) {
	opts := Options{Enabled: true}
	if got := Compile("el deploy salió bien punto final", opts).Text(); got != "el deploy salió bien." {
		t.Fatalf("esperaba \"el deploy salió bien.\", vino %q", got)
	}
	if got := Compile("dale palabra coma después vemos", opts).Text(); got != "dale, después vemos" {
		t.Fatalf("esperaba \"dale, después vemos\", vino %q", got)
	}

	entries := Entries(opts)
	for _, key := range []string{"punto final", "palabra coma"} {
		e := find(t, entries, key)
		if !e.Builtin || e.Changed {
			t.Fatalf("%q tiene que venir de fábrica y sin tocar: %+v", key, e)
		}
	}

	// La frase más larga sigue ganando: "punto final" no puede comerse el
	// "punto" de "punto y coma", ni "coma" el de "palabra coma".
	if got := Compile("uno punto y coma dos", opts).Text(); got != "uno; dos" {
		t.Fatalf("esperaba \"uno; dos\", vino %q", got)
	}
}

// Un reemplazo escrito sin tildes es el mismo comando que el de fábrica, y
// tiene que pisarlo en vez de aparecer al lado como si fueran dos.
func TestEntriesNoDuplicaPorLasTildes(t *testing.T) {
	entries := Entries(Options{Enabled: true, Replacements: map[string]string{"Guion": "—"}})
	if len(entries) != len(catalog) {
		t.Fatalf("esperaba %d filas, vinieron %d", len(catalog), len(entries))
	}
	e := find(t, entries, "guion")
	if e.Say != "Guion" || e.Writes != "—" || e.Default != "-" {
		t.Fatalf("el guión no quedó pisado: %+v", e)
	}
}

// Lo que la ventana muestra tiene que ser lo que el motor va a hacer: si acá
// dice que "dos puntos" escribe un dos puntos, dictarlo tiene que escribirlo.
func TestEntriesCoincideConLoQueCompila(t *testing.T) {
	opts := Options{Enabled: true, Replacements: map[string]string{
		"dos puntos": ":",
		"barra":      "",
	}}
	if got := Compile("mirá esto dos puntos", opts).Text(); got != "mirá esto:" {
		t.Fatalf("esperaba \"mirá esto:\", vino %q", got)
	}
	if got := Compile("carpeta barra archivo", opts).Text(); got != "carpeta barra archivo" {
		t.Fatalf("la barra apagada tiene que quedar como palabra, vino %q", got)
	}
	if find(t, Entries(opts), "barra").Off != true {
		t.Fatal("la ventana tiene que mostrar la barra apagada")
	}
}
