package cli

import (
	"fmt"
	"os"

	"github.com/neitanod/dictador/internal/history"
)

func cmdHistory(opts *options, args []string) int {
	fs := subflags("history", opts, opts.out.stderr)
	limit := fs.Int("limit", 20, "cuántos mostrar")
	fs.IntVar(limit, "n", 20, "cuántos mostrar")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	opts.refresh()

	entries, err := history.Load(*limit)
	if err != nil {
		if os.IsNotExist(err) {
			fmt.Fprintln(opts.out.stderr, "todavía no dictaste nada")
			return 1
		}
		opts.out.fail(err, "HISTORY")
		return 1
	}

	lines := make([]string, 0, len(entries))
	for _, e := range entries {
		lines = append(lines, fmt.Sprintf("%s  [%s]  %s", e.At, orElse(e.Target, "?"), e.Text))
	}
	_ = opts.out.print(entries, lines)
	return 0
}
