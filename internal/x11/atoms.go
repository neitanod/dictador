package x11

import (
	"sync"

	"github.com/jezek/xgb/xproto"
)

// atomCache evita ir al servidor por el mismo átomo mil veces.
type atomCache struct {
	mu    sync.Mutex
	atoms map[string]xproto.Atom
}

var atoms = atomCache{atoms: map[string]xproto.Atom{}}

// Atom busca (o crea) el átomo con ese nombre.
func (c *Conn) Atom(name string) (xproto.Atom, error) {
	atoms.mu.Lock()
	if a, ok := atoms.atoms[name]; ok {
		atoms.mu.Unlock()
		return a, nil
	}
	atoms.mu.Unlock()

	reply, err := xproto.InternAtom(c.X, false, uint16(len(name)), name).Reply()
	if err != nil {
		return 0, err
	}
	atoms.mu.Lock()
	atoms.atoms[name] = reply.Atom
	atoms.mu.Unlock()
	return reply.Atom, nil
}
