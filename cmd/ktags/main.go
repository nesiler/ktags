// Command ktags installs and operates customer Kubernetes clusters from the operator's laptop.
package main

import (
	"os"

	"github.com/nesiler/ktags/internal/app"
)

// version is set by GoReleaser through -ldflags at release time.
var version = "dev"

func main() {
	os.Exit(app.Run(version, os.Args[1:], os.Stdout, os.Stderr))
}
