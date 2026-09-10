package actions_test

import (
	"encoding/json"
	"os/exec"
	"strings"
	"testing"
)

const module = "github.com/nesiler/ktags"

func TestCorePackagesDoNotDependOnAdapters(t *testing.T) {
	protected := []string{
		module + "/internal/actions",
		module + "/internal/core/domain",
		module + "/internal/mask",
	}
	forbidden := []string{
		module + "/internal/cli",
		module + "/internal/tui",
		module + "/internal/transport",
		"charm.land/bubbletea",
		"github.com/spf13/cobra",
		// Infrastructure implementations: actions reach them only through interfaces.
		module + "/internal/ansible",
		module + "/internal/ssh",
		module + "/internal/kube",
		module + "/internal/rancher",
		"golang.org/x/crypto/ssh",
		"k8s.io",
		"sigs.k8s.io",
		"github.com/rancher",
		"os/exec",
	}

	for _, packagePath := range protected {
		t.Run(strings.TrimPrefix(packagePath, module+"/"), func(t *testing.T) {
			imports := packageDependencies(t, packagePath)
			for _, dependency := range imports {
				for _, forbiddenRoot := range forbidden {
					if dependency == forbiddenRoot || strings.HasPrefix(dependency, forbiddenRoot+"/") {
						t.Fatalf("%s must not depend on adapter package %s", packagePath, dependency)
					}
				}
			}
		})
	}
}

// The mask runs at every output boundary, so it must be importable by any package without
// pulling anything but the standard library along (issue #33).
func TestMaskUsesOnlyStandardLibrary(t *testing.T) {
	packagePath := module + "/internal/mask"
	command := exec.Command("go", "list", "-deps", "-json", packagePath)
	output, err := command.Output()
	if err != nil {
		t.Fatalf("list dependencies for %s: %v", packagePath, err)
	}

	decoder := json.NewDecoder(strings.NewReader(string(output)))
	for decoder.More() {
		var listedPackage struct {
			ImportPath string
			Standard   bool
		}
		if err := decoder.Decode(&listedPackage); err != nil {
			t.Fatalf("decode dependency for %s: %v", packagePath, err)
		}
		if !listedPackage.Standard && listedPackage.ImportPath != packagePath {
			t.Fatalf("%s must depend on the standard library only, found %s", packagePath, listedPackage.ImportPath)
		}
	}
}

func packageDependencies(t *testing.T, packagePath string) []string {
	t.Helper()
	command := exec.Command("go", "list", "-deps", "-json", packagePath)
	output, err := command.Output()
	if err != nil {
		t.Fatalf("list dependencies for %s: %v", packagePath, err)
	}

	decoder := json.NewDecoder(strings.NewReader(string(output)))
	var imports []string
	for decoder.More() {
		var listedPackage struct {
			ImportPath string
		}
		if err := decoder.Decode(&listedPackage); err != nil {
			t.Fatalf("decode dependency for %s: %v", packagePath, err)
		}
		imports = append(imports, listedPackage.ImportPath)
	}
	return imports
}
