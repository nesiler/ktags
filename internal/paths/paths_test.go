package paths_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nesiler/ktags/internal/paths"
)

const macHome = "/Users/operator"

func env(vars map[string]string, home string) paths.Env {
	return paths.Env{
		Getenv: func(key string) string { return vars[key] },
		Home: func() (string, error) {
			if home == "" {
				return "", errors.New("$HOME is not defined")
			}
			return home, nil
		},
	}
}

type want struct {
	config, data, state, runtime             string
	configSet, dataSet, stateSet, runtimeSet string
}

func TestResolve(t *testing.T) {
	tests := []struct {
		name string
		vars map[string]string
		home string
		want want
	}{
		{
			name: "macOS defaults use XDG locations below home",
			home: macHome,
			want: want{
				config: macHome + "/.config/ktags", configSet: "HOME",
				data: macHome + "/.local/share/ktags", dataSet: "HOME",
				state: macHome + "/.local/state/ktags", stateSet: "HOME",
				runtime: macHome + "/.local/state/ktags/run", runtimeSet: "HOME",
			},
		},
		{
			name: "XDG variables",
			home: macHome,
			vars: map[string]string{
				"XDG_CONFIG_HOME": "/xdg/config",
				"XDG_DATA_HOME":   "/xdg/data",
				"XDG_STATE_HOME":  "/xdg/state/",
				"XDG_RUNTIME_DIR": "/run/user/501",
			},
			want: want{
				config: "/xdg/config/ktags", configSet: "XDG_CONFIG_HOME",
				data: "/xdg/data/ktags", dataSet: "XDG_DATA_HOME",
				state: "/xdg/state/ktags", stateSet: "XDG_STATE_HOME",
				runtime: "/run/user/501/ktags", runtimeSet: "XDG_RUNTIME_DIR",
			},
		},
		{
			name: "runtime falls back below an XDG state root",
			home: macHome,
			vars: map[string]string{"XDG_STATE_HOME": "/xdg/state"},
			want: want{
				config: macHome + "/.config/ktags", configSet: "HOME",
				data: macHome + "/.local/share/ktags", dataSet: "HOME",
				state: "/xdg/state/ktags", stateSet: "XDG_STATE_HOME",
				runtime: "/xdg/state/ktags/run", runtimeSet: "XDG_STATE_HOME",
			},
		},
		{
			name: "relative XDG values are ignored",
			home: macHome,
			vars: map[string]string{
				"XDG_CONFIG_HOME": "xdg/config",
				"XDG_DATA_HOME":   "./data",
				"XDG_STATE_HOME":  "~/state",
				"XDG_RUNTIME_DIR": "run",
			},
			want: want{
				config: macHome + "/.config/ktags", configSet: "HOME",
				data: macHome + "/.local/share/ktags", dataSet: "HOME",
				state: macHome + "/.local/state/ktags", stateSet: "HOME",
				runtime: macHome + "/.local/state/ktags/run", runtimeSet: "HOME",
			},
		},
		{
			name: "empty values count as unset",
			home: macHome,
			vars: map[string]string{
				"KTAGS_HOME": "", "KTAGS_CONFIG_DIR": "", "KTAGS_DATA_DIR": "",
				"KTAGS_STATE_DIR": "", "KTAGS_RUNTIME_DIR": "",
				"XDG_CONFIG_HOME": "", "XDG_DATA_HOME": "", "XDG_STATE_HOME": "", "XDG_RUNTIME_DIR": "",
			},
			want: want{
				config: macHome + "/.config/ktags", configSet: "HOME",
				data: macHome + "/.local/share/ktags", dataSet: "HOME",
				state: macHome + "/.local/state/ktags", stateSet: "HOME",
				runtime: macHome + "/.local/state/ktags/run", runtimeSet: "HOME",
			},
		},
		{
			name: "KTAGS_HOME wins over XDG and needs no home",
			vars: map[string]string{
				"KTAGS_HOME":      "/opt/ktags/",
				"XDG_CONFIG_HOME": "/xdg/config",
				"XDG_RUNTIME_DIR": "/run/user/501",
			},
			want: want{
				config: "/opt/ktags/config", configSet: "KTAGS_HOME",
				data: "/opt/ktags/data", dataSet: "KTAGS_HOME",
				state: "/opt/ktags/state", stateSet: "KTAGS_HOME",
				runtime: "/opt/ktags/runtime", runtimeSet: "KTAGS_HOME",
			},
		},
		{
			name: "specific overrides win over KTAGS_HOME and XDG",
			vars: map[string]string{
				"KTAGS_HOME":        "/opt/ktags",
				"KTAGS_CONFIG_DIR":  "/k/config",
				"KTAGS_DATA_DIR":    "/k/data",
				"KTAGS_STATE_DIR":   "/k/state",
				"KTAGS_RUNTIME_DIR": "/k/run/../runtime",
				"XDG_CONFIG_HOME":   "/xdg/config",
				"XDG_RUNTIME_DIR":   "/run/user/501",
			},
			want: want{
				config: "/k/config", configSet: "KTAGS_CONFIG_DIR",
				data: "/k/data", dataSet: "KTAGS_DATA_DIR",
				state: "/k/state", stateSet: "KTAGS_STATE_DIR",
				runtime: "/k/runtime", runtimeSet: "KTAGS_RUNTIME_DIR",
			},
		},
		{
			name: "runtime falls back below an overridden state root",
			vars: map[string]string{
				"KTAGS_CONFIG_DIR": "/k/config",
				"KTAGS_DATA_DIR":   "/k/data",
				"KTAGS_STATE_DIR":  "/k/state",
			},
			want: want{
				config: "/k/config", configSet: "KTAGS_CONFIG_DIR",
				data: "/k/data", dataSet: "KTAGS_DATA_DIR",
				state: "/k/state", stateSet: "KTAGS_STATE_DIR",
				runtime: "/k/state/run", runtimeSet: "KTAGS_STATE_DIR",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			roots, err := paths.Resolve(env(tt.vars, tt.home))
			if err != nil {
				t.Fatalf("Resolve: %v", err)
			}
			got := want{
				config: roots.Config.Path, configSet: roots.Config.Setting,
				data: roots.Data.Path, dataSet: roots.Data.Setting,
				state: roots.State.Path, stateSet: roots.State.Setting,
				runtime: roots.Runtime.Path, runtimeSet: roots.Runtime.Setting,
			}
			if got != tt.want {
				t.Fatalf("roots\n got  %+v\n want %+v", got, tt.want)
			}
		})
	}
}

func TestResolveRefusesRelative(t *testing.T) {
	tests := []struct {
		setting string
		value   string
		home    string
	}{
		{setting: "KTAGS_HOME", value: "ktags"},
		{setting: "KTAGS_CONFIG_DIR", value: "config"},
		{setting: "KTAGS_DATA_DIR", value: "./data"},
		{setting: "KTAGS_STATE_DIR", value: "~/state"},
		{setting: "KTAGS_RUNTIME_DIR", value: "../run"},
		{setting: "HOME", home: "operator"},
	}

	for _, tt := range tests {
		t.Run(tt.setting, func(t *testing.T) {
			vars := map[string]string{}
			home := macHome
			if tt.setting == "HOME" {
				home = tt.home
			} else {
				vars[tt.setting] = tt.value
			}
			_, err := paths.Resolve(env(vars, home))
			var pathErr *paths.Error
			if !errors.As(err, &pathErr) {
				t.Fatalf("Resolve error = %v, want *paths.Error", err)
			}
			if pathErr.Setting != tt.setting {
				t.Fatalf("Setting = %q, want %q", pathErr.Setting, tt.setting)
			}
			if !strings.Contains(err.Error(), "must be an absolute path") {
				t.Fatalf("error = %q, want the absolute-path reason", err)
			}
		})
	}
}

func TestResolveRequiresHomeForDefaults(t *testing.T) {
	_, err := paths.Resolve(env(nil, ""))
	var pathErr *paths.Error
	if !errors.As(err, &pathErr) || pathErr.Setting != "HOME" {
		t.Fatalf("Resolve error = %v, want a HOME *paths.Error", err)
	}
	if !strings.Contains(err.Error(), "next: set HOME") {
		t.Fatalf("error = %q, want a corrective action", err)
	}
}

func TestResolveHasNoSideEffects(t *testing.T) {
	root := filepath.Join(t.TempDir(), "absent")
	roots, err := paths.Resolve(env(map[string]string{"KTAGS_HOME": root}, ""))
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if roots.Config.Path != filepath.Join(root, "config") {
		t.Fatalf("config = %q, want below %q", roots.Config.Path, root)
	}
	if _, err := os.Lstat(root); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("Resolve created %q (stat err %v); it must not touch the filesystem", root, err)
	}
}

func TestEnsureCreatesPrivateRoots(t *testing.T) {
	roots := tempRoots(t)
	if err := paths.Ensure(context.Background(), roots); err != nil {
		t.Fatalf("Ensure: %v", err)
	}
	assertPrivate(t, roots)
}

func TestEnsureIsIdempotent(t *testing.T) {
	roots := tempRoots(t)
	for i := range 2 {
		if err := paths.Ensure(context.Background(), roots); err != nil {
			t.Fatalf("Ensure call %d: %v", i+1, err)
		}
	}
	assertPrivate(t, roots)
}

func TestEnsureRefusesLoosePermissions(t *testing.T) {
	for _, mode := range []os.FileMode{0o755, 0o750, 0o701, 0o770} {
		t.Run(mode.String(), func(t *testing.T) {
			roots := tempRoots(t)
			loose := roots.Data.Path
			if err := os.MkdirAll(loose, 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.Chmod(loose, mode); err != nil {
				t.Fatal(err)
			}

			err := paths.Ensure(context.Background(), roots)
			var pathErr *paths.Error
			if !errors.As(err, &pathErr) || pathErr.Setting != "KTAGS_HOME" {
				t.Fatalf("Ensure error = %v, want a KTAGS_HOME *paths.Error", err)
			}
			if !strings.Contains(err.Error(), "next: chmod 700") {
				t.Fatalf("error = %q, want the chmod action", err)
			}
			info, statErr := os.Stat(loose)
			if statErr != nil {
				t.Fatal(statErr)
			}
			if info.Mode().Perm() != mode {
				t.Fatalf("mode = %v, want %v left unchanged", info.Mode().Perm(), mode)
			}
		})
	}
}

func TestEnsureRefusesNonDirectory(t *testing.T) {
	roots := tempRoots(t)
	if err := os.MkdirAll(filepath.Dir(roots.Config.Path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(roots.Config.Path, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	err := paths.Ensure(context.Background(), roots)
	var pathErr *paths.Error
	if !errors.As(err, &pathErr) || pathErr.Setting != "KTAGS_HOME" {
		t.Fatalf("Ensure error = %v, want a KTAGS_HOME *paths.Error", err)
	}
}

func TestEnsureHonoursCancellation(t *testing.T) {
	roots := tempRoots(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := paths.Ensure(ctx, roots); !errors.Is(err, context.Canceled) {
		t.Fatalf("Ensure error = %v, want context.Canceled", err)
	}
	if _, err := os.Lstat(roots.Config.Path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("Ensure created %q after cancellation", roots.Config.Path)
	}
}

func TestErrorsDoNotExposeOtherSettings(t *testing.T) {
	base := t.TempDir()
	others := map[string]string{
		"HOME":            "/marker-home",
		"KTAGS_HOME":      "/marker-ktags-home",
		"KTAGS_DATA_DIR":  filepath.Join(base, "marker-data"),
		"KTAGS_STATE_DIR": filepath.Join(base, "marker-state"),
		"XDG_CONFIG_HOME": "/marker-xdg-config",
		"XDG_RUNTIME_DIR": "/marker-xdg-runtime",
	}

	t.Run("resolve", func(t *testing.T) {
		vars := map[string]string{"KTAGS_CONFIG_DIR": "relative/config"}
		for key, value := range others {
			vars[key] = value
		}
		_, err := paths.Resolve(env(vars, others["HOME"]))
		assertOnlyNames(t, err, "KTAGS_CONFIG_DIR", others)
	})

	t.Run("ensure", func(t *testing.T) {
		loose := filepath.Join(base, "config")
		if err := os.Mkdir(loose, 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.Chmod(loose, 0o755); err != nil {
			t.Fatal(err)
		}
		vars := map[string]string{"KTAGS_CONFIG_DIR": loose}
		for key, value := range others {
			vars[key] = value
		}
		roots, err := paths.Resolve(env(vars, others["HOME"]))
		if err != nil {
			t.Fatalf("Resolve: %v", err)
		}
		assertOnlyNames(t, paths.Ensure(context.Background(), roots), "KTAGS_CONFIG_DIR", others)
	})
}

func assertOnlyNames(t *testing.T, err error, setting string, others map[string]string) {
	t.Helper()
	if err == nil {
		t.Fatal("error = nil, want a rejection")
	}
	msg := err.Error()
	if !strings.HasPrefix(msg, setting+": ") {
		t.Fatalf("error = %q, want it to start with %s", msg, setting)
	}
	if !strings.Contains(msg, "\n  next: ") {
		t.Fatalf("error = %q, want a corrective action", msg)
	}
	for key, value := range others {
		if strings.Contains(msg, value) {
			t.Fatalf("error = %q exposes the value of %s", msg, key)
		}
	}
}

func tempRoots(t *testing.T) paths.Roots {
	t.Helper()
	roots, err := paths.Resolve(env(map[string]string{"KTAGS_HOME": filepath.Join(t.TempDir(), "ktags")}, ""))
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	return roots
}

func assertPrivate(t *testing.T, roots paths.Roots) {
	t.Helper()
	for _, root := range roots.All() {
		info, err := os.Stat(root.Path)
		if err != nil {
			t.Fatalf("%s root: %v", root.Name, err)
		}
		if !info.IsDir() || info.Mode().Perm() != 0o700 {
			t.Fatalf("%s root %q mode = %v, want drwx------", root.Name, root.Path, info.Mode())
		}
	}
}
