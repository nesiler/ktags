package paths

import (
	"fmt"
	"os"
	"path/filepath"
)

const appDir = "ktags"

// Env is the process environment Resolve reads. Tests inject fakes; OSEnv reads the real one.
type Env struct {
	// Getenv returns the value of an environment variable, or "" when it is unset.
	Getenv func(key string) string
	// Home returns the operator's home directory. It is only called when a root falls back to
	// its home-relative default.
	Home func() (string, error)
}

// OSEnv returns the environment of the running process.
func OSEnv() Env {
	return Env{Getenv: os.Getenv, Home: os.UserHomeDir}
}

// Root is one resolved directory and the setting that decided it.
type Root struct {
	// Name is the role of the root: config, data, state or runtime.
	Name string
	// Path is absolute and cleaned.
	Path string
	// Setting is the environment variable the path came from (HOME for built-in defaults).
	// Errors about this root name it, and only it.
	Setting string
	// Override is the ktags variable an operator sets to move this root.
	Override string
}

// Roots are the four ktags filesystem roots.
type Roots struct {
	Config  Root
	Data    Root
	State   Root
	Runtime Root
}

// All returns the roots in creation order; runtime may live below state.
func (r Roots) All() []Root {
	return []Root{r.Config, r.Data, r.State, r.Runtime}
}

// Overrides returns the environment assignments ("KTAGS_CONFIG_DIR=/…") that make Resolve return
// these roots whatever else the environment holds. A process started outside the operator's
// shell, such as the launchd service, gets them so it uses the same roots as its client.
func (r Roots) Overrides() []string {
	var out []string
	for _, root := range r.All() {
		out = append(out, root.Override+"="+root.Path)
	}
	return out
}

// Resolve computes the roots from env without touching the filesystem.
//
// Precedence for config, data and state: the specific KTAGS_*_DIR override, then KTAGS_HOME,
// then the XDG variable, then the default below $HOME. XDG locations are used on macOS too.
// Runtime uses KTAGS_RUNTIME_DIR, then KTAGS_HOME/runtime, then $XDG_RUNTIME_DIR/ktags, then
// <state>/run. Empty variables count as unset. A relative KTAGS_* value is refused; a relative
// XDG value is ignored, as the XDG base directory specification requires.
func Resolve(env Env) (Roots, error) {
	r := resolver{env: env}
	home, err := r.override("KTAGS_HOME")
	if err != nil {
		return Roots{}, err
	}

	var roots Roots
	for _, spec := range []struct {
		dst      *Root
		name     string
		override string
		xdg      string
		fallback string
	}{
		{&roots.Config, "config", "KTAGS_CONFIG_DIR", "XDG_CONFIG_HOME", ".config"},
		{&roots.Data, "data", "KTAGS_DATA_DIR", "XDG_DATA_HOME", filepath.Join(".local", "share")},
		{&roots.State, "state", "KTAGS_STATE_DIR", "XDG_STATE_HOME", filepath.Join(".local", "state")},
	} {
		root, err := r.root(spec.name, spec.override, home, spec.xdg, spec.fallback)
		if err != nil {
			return Roots{}, err
		}
		*spec.dst = root
	}

	roots.Runtime, err = r.runtime(home, roots.State)
	if err != nil {
		return Roots{}, err
	}
	return roots, nil
}

type resolver struct {
	env Env
}

// override returns a cleaned absolute KTAGS_* value, "" when unset, or an error when relative.
func (r resolver) override(key string) (string, error) {
	value := r.env.Getenv(key)
	if value == "" {
		return "", nil
	}
	if !filepath.IsAbs(value) {
		return "", &Error{
			Setting: key,
			Problem: fmt.Sprintf("must be an absolute path, got %q", value),
			Next:    fmt.Sprintf("set %s to an absolute path, or unset it to use the default", key),
		}
	}
	return filepath.Clean(value), nil
}

// xdg returns a cleaned absolute XDG value, or "" when it is unset, empty or relative.
func (r resolver) xdg(key string) string {
	value := r.env.Getenv(key)
	if value == "" || !filepath.IsAbs(value) {
		return ""
	}
	return filepath.Clean(value)
}

func (r resolver) root(name, override, home, xdg, fallback string) (Root, error) {
	root := Root{Name: name, Override: override}
	value, err := r.override(override)
	switch {
	case err != nil:
		return Root{}, err
	case value != "":
		root.Path, root.Setting = value, override
	case home != "":
		root.Path, root.Setting = filepath.Join(home, name), "KTAGS_HOME"
	case r.xdg(xdg) != "":
		root.Path, root.Setting = filepath.Join(r.xdg(xdg), appDir), xdg
	default:
		userHome, err := r.home(override)
		if err != nil {
			return Root{}, err
		}
		root.Path, root.Setting = filepath.Join(userHome, fallback, appDir), "HOME"
	}
	return root, nil
}

func (r resolver) runtime(home string, state Root) (Root, error) {
	const override = "KTAGS_RUNTIME_DIR"
	root := Root{Name: "runtime", Override: override}
	value, err := r.override(override)
	switch {
	case err != nil:
		return Root{}, err
	case value != "":
		root.Path, root.Setting = value, override
	case home != "":
		root.Path, root.Setting = filepath.Join(home, "runtime"), "KTAGS_HOME"
	case r.xdg("XDG_RUNTIME_DIR") != "":
		root.Path, root.Setting = filepath.Join(r.xdg("XDG_RUNTIME_DIR"), appDir), "XDG_RUNTIME_DIR"
	default:
		root.Path, root.Setting = filepath.Join(state.Path, "run"), state.Setting
	}
	return root, nil
}

// home returns the operator's home directory for a default location that override would replace.
func (r resolver) home(override string) (string, error) {
	next := fmt.Sprintf("set HOME to your home directory, or set %s or KTAGS_HOME to an absolute path", override)
	if r.env.Home == nil {
		return "", &Error{Setting: "HOME", Problem: "is not available", Next: next}
	}
	value, err := r.env.Home()
	if err != nil || value == "" {
		// The underlying error is dropped on purpose: it may quote other variables.
		return "", &Error{Setting: "HOME", Problem: "is not set", Next: next}
	}
	if !filepath.IsAbs(value) {
		return "", &Error{
			Setting: "HOME",
			Problem: fmt.Sprintf("must be an absolute path, got %q", value),
			Next:    next,
		}
	}
	return filepath.Clean(value), nil
}
