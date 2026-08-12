package cli

import (
	"fmt"
	"strings"

	"github.com/neitanod/dictador/internal/x11"
)

func cmdKeys(opts *options, args []string) int {
	fs := subflags("keys", opts, opts.out.stderr)
	if err := fs.Parse(args); err != nil {
		return 2
	}
	opts.refresh()
	filter := strings.ToLower(firstArg(fs.Args(), ""))

	conn, err := x11.Open()
	if err != nil {
		opts.out.fail(err, "X11")
		return 1
	}
	defer conn.Close()

	keymap, err := conn.LoadKeymap()
	if err != nil {
		opts.out.fail(err, "X11")
		return 1
	}

	keys := keymap.Keys()
	if filter != "" {
		var kept []x11.Key
		for _, k := range keys {
			if strings.Contains(strings.ToLower(k.Name), filter) {
				kept = append(kept, k)
			}
		}
		keys = kept
	}

	lines := make([]string, 0, len(keys))
	for _, k := range keys {
		lines = append(lines, fmt.Sprintf("%4d  %s", k.Keycode, k.Name))
	}
	_ = opts.out.print(map[string]any{"keys": keys}, lines)
	return 0
}
