package tui

import (
	"context"
	"fmt"
	"io"
	"time"

	tea "charm.land/bubbletea/v2"
	zone "github.com/lrstanley/bubblezone/v2"
)

// DefaultPoll is how often the TUI refreshes from the service.
const DefaultPoll = 2 * time.Second

// Run shows the TUI on the terminal until the operator quits, then writes the exit line to
// out. Runs started from the TUI continue in the service.
func Run(ctx context.Context, opts Options, out io.Writer) error {
	if opts.Poll == 0 {
		opts.Poll = DefaultPoll
	}
	if opts.Zones == nil {
		opts.Zones = zone.New()
		defer opts.Zones.Close()
	}
	final, err := tea.NewProgram(New(opts), tea.WithContext(ctx)).Run()
	finish(final, out)
	return err
}

// finish prints the exit line of the final model, if it has one.
func finish(final tea.Model, out io.Writer) {
	if m, ok := final.(Model); ok && m.ExitLine() != "" {
		_, _ = fmt.Fprintln(out, "ktags: "+m.ExitLine())
	}
}
