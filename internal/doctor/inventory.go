package doctor

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"

	"github.com/nesiler/ktags/internal/inventory"
)

func init() {
	register(Check{ID: "inventory", Title: "every customer record loads", Order: 40, Run: checkInventory})
}

// checkInventory loads every customer record. A refused record is its own row, named after the
// customer directory, so each carries the one fix for its file.
func checkInventory(ctx context.Context, env Env) []Result {
	dir := inventory.CustomersDir(env.Roots.Data.Path)
	entries, err := os.ReadDir(dir)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return []Result{{Status: StatusOK, Evidence: "no customers yet: " + dir + " does not exist"}}
	case err != nil:
		return []Result{{Status: StatusFail, Evidence: fmt.Sprintf("cannot list %s: %v", dir, errors.Unwrap(err)), Fix: "chmod 700 " + quote(dir)}}
	}
	var refused []Result
	loaded := 0
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		customer := inventory.CustomerDir(env.Roots.Data.Path, entry.Name())
		if _, err := inventory.Load(ctx, customer); err != nil {
			refused = append(refused, Result{
				ID:       "inventory." + entry.Name(),
				Title:    "the customer record of " + entry.Name() + " loads",
				Status:   StatusFail,
				Evidence: firstLine(err.Error()),
				// Unquoted, so an EDITOR with arguments ("code -w") works.
				Fix: "${EDITOR:-vi} " + quote(inventory.RecordPath(customer)),
			})
			continue
		}
		loaded++
	}
	if len(refused) > 0 {
		return refused
	}
	return []Result{{Status: StatusOK, Evidence: fmt.Sprintf("%d customer record(s) load from %s", loaded, dir)}}
}
