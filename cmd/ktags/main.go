// Command ktags installs and operates customer Kubernetes clusters from the operator's laptop.
//
// This is the entry point placeholder; the core is ported from ops-platform in Phase 1.
package main

import (
	"fmt"
	"os"
)

// version is set by GoReleaser through -ldflags at release time.
var version = "dev"

func main() {
	if len(os.Args) > 1 && (os.Args[1] == "version" || os.Args[1] == "--version") {
		fmt.Println("ktags", version)
		return
	}
	fmt.Fprintln(os.Stderr, "ktags: nothing to run yet; see docs/STATUS.md")
	os.Exit(2)
}
