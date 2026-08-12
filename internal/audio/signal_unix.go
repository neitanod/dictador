//go:build !windows

package audio

import (
	"os"
	"syscall"
)

// interruptSignal es lo que se le manda a parec para que cierre ordenado.
var interruptSignal os.Signal = syscall.SIGTERM
