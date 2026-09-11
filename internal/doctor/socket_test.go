package doctor

import (
	"strings"

	"github.com/nesiler/ktags/internal/service"
)

// longSocket is a stopped service whose socket path is n bytes long.
func longSocket(n int) service.Status {
	return service.Status{Socket: "/" + strings.Repeat("s", n-1)}
}
