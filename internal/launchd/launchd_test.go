package launchd

import (
	"context"
	"encoding/xml"
	"errors"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"github.com/nesiler/ktags/internal/paths"
	"github.com/nesiler/ktags/internal/service"
)

// fakeLaunchctl records every launchctl call. print succeeds when loaded is set; the command
// named in fail fails with its output.
type fakeLaunchctl struct {
	loaded bool
	fail   string
	// quiet makes the failure print nothing.
	quiet bool
	calls [][]string
}

func (f *fakeLaunchctl) run(_ context.Context, args ...string) ([]byte, error) {
	f.calls = append(f.calls, args)
	switch {
	case args[0] == f.fail && f.quiet:
		return nil, errors.New("exit status 5")
	case args[0] == f.fail:
		return []byte("Bootstrap failed: 5: Input/output error\n"), errors.New("exit status 5")
	case args[0] == "print" && !f.loaded:
		return []byte("Could not find service"), errors.New("exit status 113")
	}
	return nil, nil
}

func testRoots(t *testing.T, home string) paths.Roots {
	t.Helper()
	roots, err := paths.Resolve(paths.Env{Getenv: func(k string) string {
		if k == "KTAGS_HOME" {
			return home
		}
		return ""
	}})
	if err != nil {
		t.Fatal(err)
	}
	if err := paths.Ensure(context.Background(), roots); err != nil {
		t.Fatal(err)
	}
	return roots
}

func testLaunchd(t *testing.T, fake *fakeLaunchctl) (Agent, paths.Roots) {
	t.Helper()
	roots := testRoots(t, t.TempDir())
	l := New("/opt/ktags & co/bin/ktags", roots)
	l.Launchctl = fake.run
	return l, roots
}

// plistDoc is the part of the definition the tests read back through a real XML parser.
type plistDoc struct {
	Dict struct {
		Items []plistItem `xml:",any"`
	} `xml:"dict"`
}

type plistItem struct {
	XMLName xml.Name
	Text    string      `xml:",chardata"`
	Items   []plistItem `xml:",any"`
}

// entries maps each top-level key to its value element.
func (d plistDoc) entries(t *testing.T) map[string]plistItem {
	t.Helper()
	out := map[string]plistItem{}
	items := d.Dict.Items
	for i := 0; i+1 < len(items); i += 2 {
		if items[i].XMLName.Local != "key" {
			t.Fatalf("plist item %d is <%s>, want <key>", i, items[i].XMLName.Local)
		}
		out[items[i].Text] = items[i+1]
	}
	return out
}

func readPlist(t *testing.T, path string) plistDoc {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	dec := xml.NewDecoder(strings.NewReader(string(data)))
	dec.Strict = true
	var doc plistDoc
	if err := dec.Decode(&doc); err != nil {
		t.Fatalf("definition is not well-formed XML: %v\n%s", err, data)
	}
	for {
		tok, err := dec.Token()
		if errors.Is(err, io.EOF) {
			break
		}
		if text, ok := tok.(xml.CharData); err != nil || !ok || strings.TrimSpace(string(text)) != "" {
			t.Fatalf("data after the plist element: %v %v", tok, err)
		}
	}
	return doc
}

// #12-K1: start on macOS writes a private definition that runs `ktags service run` with the
// client's roots and loads it with bootstrap when launchd does not know the job.
func TestLaunchdBootstrapsWhenNotLoaded(t *testing.T) {
	fake := &fakeLaunchctl{}
	l, roots := testLaunchd(t, fake)
	if err := l.Launch(context.Background()); err != nil {
		t.Fatalf("Launch: %v", err)
	}
	target := "gui/" + strconv.Itoa(os.Getuid()) + "/" + l.Label
	want := [][]string{{"print", target}, {"bootstrap", "gui/" + strconv.Itoa(os.Getuid()), l.Definition}}
	if !reflect.DeepEqual(fake.calls, want) {
		t.Fatalf("launchctl calls %q, want %q", fake.calls, want)
	}

	info, err := os.Stat(l.Definition)
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("definition %v, %v; want mode 0600", info, err)
	}
	if dir, err := os.Stat(filepath.Dir(l.Definition)); err != nil || dir.Mode().Perm() != 0o700 {
		t.Fatalf("definition directory %v, %v; want mode 0700", dir, err)
	}
	if !strings.HasPrefix(l.Definition, roots.State.Path+string(os.PathSeparator)) {
		t.Fatalf("definition %s is outside the state root %s", l.Definition, roots.State.Path)
	}

	e := readPlist(t, l.Definition).entries(t)
	if e["Label"].Text != l.Label {
		t.Fatalf("Label %q, want %q", e["Label"].Text, l.Label)
	}
	var args []string
	for _, item := range e["ProgramArguments"].Items {
		args = append(args, item.Text)
	}
	if !reflect.DeepEqual(args, []string{"/opt/ktags & co/bin/ktags", "service", "run"}) {
		t.Fatalf("ProgramArguments %q", args)
	}
	env := map[string]string{}
	items := e["EnvironmentVariables"].Items
	for i := 0; i+1 < len(items); i += 2 {
		env[items[i].Text] = items[i+1].Text
	}
	wantEnv := map[string]string{
		"KTAGS_CONFIG_DIR": roots.Config.Path, "KTAGS_DATA_DIR": roots.Data.Path,
		"KTAGS_STATE_DIR": roots.State.Path, "KTAGS_RUNTIME_DIR": roots.Runtime.Path,
	}
	if !reflect.DeepEqual(env, wantEnv) {
		t.Fatalf("EnvironmentVariables %v, want %v", env, wantEnv)
	}
	if e["RunAtLoad"].XMLName.Local != "true" {
		t.Fatalf("RunAtLoad is <%s>, want <true/>", e["RunAtLoad"].XMLName.Local)
	}
	keep := e["KeepAlive"].Items
	if len(keep) != 2 || keep[0].Text != "SuccessfulExit" || keep[1].XMLName.Local != "false" {
		t.Fatalf("KeepAlive %+v, want SuccessfulExit false: restart after a crash, never after a stop", keep)
	}
	if e["StandardErrorPath"].Text != l.Log || e["StandardOutPath"].Text != l.Log {
		t.Fatalf("log paths %q %q, want %q", e["StandardOutPath"].Text, e["StandardErrorPath"].Text, l.Log)
	}
}

// #68-K1 D3: a job launchd still has loaded is left alone: no bootstrap, no kickstart, and its
// definition file is not rewritten, so a stale one stays visible.
func TestLaunchdLeavesLoadedJobAlone(t *testing.T) {
	fake := &fakeLaunchctl{loaded: true}
	l, _ := testLaunchd(t, fake)
	if err := os.MkdirAll(filepath.Dir(l.Definition), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(l.Definition, []byte("old definition"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := l.Launch(context.Background()); err != nil {
		t.Fatalf("Launch: %v", err)
	}
	target := "gui/" + strconv.Itoa(os.Getuid()) + "/" + l.Label
	if want := [][]string{{"print", target}}; !reflect.DeepEqual(fake.calls, want) {
		t.Fatalf("launchctl calls %q, want %q", fake.calls, want)
	}
	if data, err := os.ReadFile(l.Definition); err != nil || string(data) != "old definition" {
		t.Fatalf("definition %q, %v; want it untouched", data, err)
	}
}

// #68-K1 D3: Stale compares the definition file with the one this build writes.
func TestLaunchdStale(t *testing.T) {
	l, _ := testLaunchd(t, &fakeLaunchctl{})
	if stale, err := l.Stale(); stale || err != nil {
		t.Fatalf("no definition: stale %v, %v; want not stale", stale, err)
	}
	if err := l.Launch(context.Background()); err != nil {
		t.Fatal(err)
	}
	if stale, err := l.Stale(); stale || err != nil {
		t.Fatalf("fresh definition: stale %v, %v; want not stale", stale, err)
	}
	other := l
	other.Program = "/opt/ktags/older/ktags"
	if stale, err := other.Stale(); !stale || err != nil {
		t.Fatalf("definition of another build: stale %v, %v; want stale", stale, err)
	}
	chmod(t, l.Definition, 0o000)
	if _, err := l.Stale(); err == nil {
		t.Fatal("unreadable definition: no error")
	}
	chmod(t, l.Definition, 0o600)
	bad := l
	bad.Env = []string{"KTAGS_HOME"}
	if _, err := bad.Stale(); err == nil {
		t.Fatal("a definition this build cannot write: no error")
	}
}

// #68-K2: the chmod hint is POSIX-quoted, so a path with $, a backtick or a quote pastes as is.
func TestLaunchdChmodHintIsShellQuoted(t *testing.T) {
	fake := &fakeLaunchctl{}
	l, _ := testLaunchd(t, fake)
	dir := filepath.Join(t.TempDir(), "it's $HOME `x`")
	l.Definition = filepath.Join(dir, "job.plist")
	if err := os.Mkdir(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	chmod(t, dir, 0o755)
	pe := wantUnavailable(t, l.Launch(context.Background()))
	if want := "chmod 700 '" + strings.ReplaceAll(dir, "'", `'\''`) + "'"; pe.Hint != want {
		t.Fatalf("hint %q, want %q", pe.Hint, want)
	}
}

func TestLaunchdUnload(t *testing.T) {
	for _, loaded := range []bool{false, true} {
		fake := &fakeLaunchctl{loaded: loaded}
		l, _ := testLaunchd(t, fake)
		if err := l.Unload(context.Background()); err != nil {
			t.Fatalf("Unload (loaded %v): %v", loaded, err)
		}
		target := "gui/" + strconv.Itoa(os.Getuid()) + "/" + l.Label
		want := [][]string{{"print", target}}
		if loaded {
			want = append(want, []string{"bootout", target})
		}
		if !reflect.DeepEqual(fake.calls, want) {
			t.Fatalf("loaded %v: launchctl calls %q, want %q", loaded, fake.calls, want)
		}
	}
}

// A launchctl failure names the command, carries launchctl's own words and the next command.
func TestLaunchdFailureIsExplained(t *testing.T) {
	fake := &fakeLaunchctl{fail: "bootstrap"}
	l, _ := testLaunchd(t, fake)
	err := l.Launch(context.Background())
	pe := wantUnavailable(t, err)
	if !strings.Contains(pe.Message, "launchctl bootstrap") || !strings.Contains(pe.Message, "Input/output error") ||
		!strings.Contains(pe.Hint, "launchctl print gui/") {
		t.Fatalf("error %q / %q", pe.Message, pe.Hint)
	}
}

// The label follows the runtime root: two ktags homes never share a launchd job.
func TestLaunchdLabelPerRuntimeRoot(t *testing.T) {
	a, b := testRoots(t, t.TempDir()), testRoots(t, t.TempDir())
	la, lb := New("/bin/ktags", a), New("/bin/ktags", b)
	if la.Label == lb.Label {
		t.Fatalf("two runtime roots share the label %s", la.Label)
	}
	if again := New("/usr/bin/ktags", a); again.Label != la.Label {
		t.Fatalf("label %s changed to %s for the same runtime root", la.Label, again.Label)
	}
	if !strings.HasPrefix(la.Label, labelPrefix+".") {
		t.Fatalf("label %s lacks the ktags prefix", la.Label)
	}
}

func TestLaunchdRefusals(t *testing.T) {
	t.Run("relative program", func(t *testing.T) {
		fake := &fakeLaunchctl{}
		l, _ := testLaunchd(t, fake)
		l.Program = "ktags"
		if err := l.Launch(context.Background()); err == nil || len(fake.calls) != 0 {
			t.Fatalf("Launch: %v, calls %q; want a refusal before launchctl", err, fake.calls)
		}
	})
	// Group bits alone and others bits alone are each refused, not only both together.
	for _, mode := range []os.FileMode{0o755, 0o750, 0o770, 0o705} {
		t.Run("definition directory mode "+mode.String(), func(t *testing.T) {
			fake := &fakeLaunchctl{}
			l, _ := testLaunchd(t, fake)
			dir := filepath.Dir(l.Definition)
			if err := os.Mkdir(dir, 0o700); err != nil {
				t.Fatal(err)
			}
			chmod(t, dir, mode)
			err := l.Launch(context.Background())
			if pe := wantUnavailable(t, err); !strings.Contains(pe.Hint, "chmod 700") || len(fake.calls) != 0 {
				t.Fatalf("Launch: %v, calls %q; want a chmod hint and no launchctl call", err, fake.calls)
			}
		})
	}
	t.Run("malformed environment entry", func(t *testing.T) {
		fake := &fakeLaunchctl{}
		l, _ := testLaunchd(t, fake)
		l.Env = []string{"KTAGS_HOME"}
		if err := l.Launch(context.Background()); err == nil || len(fake.calls) != 0 {
			t.Fatalf("Launch: %v, calls %q; want a refusal before launchctl", err, fake.calls)
		}
	})
}

// Without output from launchctl, the error names its exit status instead.
func TestLaunchdQuietFailure(t *testing.T) {
	fake := &fakeLaunchctl{fail: "bootstrap", quiet: true}
	l, _ := testLaunchd(t, fake)
	pe := wantUnavailable(t, l.Launch(context.Background()))
	if !strings.HasSuffix(pe.Message, "failed: exit status 5") {
		t.Fatalf("error %q, want the exit status", pe.Message)
	}
}

// A definition that cannot be replaced stops the launch before bootstrap.
func TestLaunchdDefinitionNotReplaceable(t *testing.T) {
	fake := &fakeLaunchctl{}
	l, _ := testLaunchd(t, fake)
	if err := os.MkdirAll(filepath.Join(l.Definition, "blocker"), 0o700); err != nil {
		t.Fatal(err)
	}
	pe := wantUnavailable(t, l.Launch(context.Background()))
	target := "gui/" + strconv.Itoa(os.Getuid()) + "/" + l.Label
	if !strings.Contains(pe.Message, "cannot replace the launchd definition") || !reflect.DeepEqual(fake.calls, [][]string{{"print", target}}) {
		t.Fatalf("Launch: %q, calls %q", pe.Message, fake.calls)
	}
}

// launchctl runs from its absolute path with an allowlisted environment, never the operator's.
func TestLaunchctlCommandEnvironment(t *testing.T) {
	t.Setenv("KTAGS_SECRET_PROBE", "tskey-example")
	cmd := launchctlCommand(context.Background(), "print", "gui/501")
	if cmd.Path != "/bin/launchctl" || !reflect.DeepEqual(cmd.Args, []string{"/bin/launchctl", "print", "gui/501"}) {
		t.Fatalf("command %s %q", cmd.Path, cmd.Args)
	}
	if want := []string{"PATH=/usr/bin:/bin:/usr/sbin:/sbin"}; !reflect.DeepEqual(cmd.Env, want) {
		t.Fatalf("environment %q, want %q", cmd.Env, want)
	}
}

func wantUnavailable(t *testing.T, err error) *service.Error {
	t.Helper()
	var pe *service.Error
	if !errors.As(err, &pe) || pe.Code != service.CodeUnavailable {
		t.Fatalf("error %v (%T), want a service error %s", err, err, service.CodeUnavailable)
	}
	return pe
}

// chmod changes a mode for the rest of the test and restores 0700 so cleanup can remove it.
func chmod(t *testing.T, path string, mode os.FileMode) {
	t.Helper()
	if err := os.Chmod(path, mode); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(path, 0o700) })
}

// Program reads back the executable from the definition that Launch writes.
func TestProgramReadsTheDefinition(t *testing.T) {
	dir := t.TempDir()
	program := "/opt/k tags/<ktags>&"
	roots, err := paths.Resolve(paths.Env{Getenv: func(key string) string {
		if key == "KTAGS_HOME" {
			return dir
		}
		return ""
	}})
	if err != nil {
		t.Fatal(err)
	}
	agent := New(program, roots)
	data, err := agent.plist()
	if err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(dir, "agent.plist")
	if err := os.WriteFile(file, data, 0o600); err != nil {
		t.Fatal(err)
	}
	if got, err := Program(file); err != nil || got != program {
		t.Fatalf("Program = %q, %v; want %q", got, err, program)
	}

	if _, err := Program(filepath.Join(dir, "missing.plist")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("missing definition: %v", err)
	}
	for name, body := range map[string]string{
		"not xml":             "<plist><dict>",
		"key cut off":         "<plist><dict><key>Program",
		"program cut off":     "<plist><dict><key>ProgramArguments</key><array><string>/bin",
		"no ProgramArguments": "<plist><dict><key>Label</key><string>x</string><key>Program</key><string>/bin/x</string></dict></plist>",
		"arguments not first": "<plist><dict><key>ProgramArguments</key><integer>1</integer><string>/bin/x</string></dict></plist>",
	} {
		if err := os.WriteFile(file, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		if got, err := Program(file); err == nil {
			t.Fatalf("%s: Program = %q, want an error", name, got)
		}
	}
}
