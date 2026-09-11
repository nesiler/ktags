package launchd

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/nesiler/ktags/internal/paths"
	"github.com/nesiler/ktags/internal/service"
	"github.com/nesiler/ktags/internal/shell"
)

const labelPrefix = "io.github.nesiler.ktags.service"

// Agent starts the service as a user launchd agent. The definition lives in the ktags state root
// and is loaded into the operator's gui domain by Launch and removed by Unload. Nothing is
// installed in ~/Library/LaunchAgents, so the service starts when ktags asks for it, not at
// login. launchd restarts a service that crashed, never one that was stopped.
type Agent struct {
	// Label is the launchd job label. It is derived from the runtime root, so two ktags homes
	// never share a job.
	Label string
	// Definition is the property list file of the job.
	Definition string
	// Program is the absolute path of the ktags executable.
	Program string
	// Env are the KTAGS_* assignments that pin the service to the client's roots.
	Env []string
	// Log receives the service's stdout and stderr.
	Log string
	UID int
	// Launchctl runs launchctl with args and returns its combined output.
	Launchctl func(ctx context.Context, args ...string) ([]byte, error)
}

// New returns the agent for the executable program and the roots.
func New(program string, roots paths.Roots) Agent {
	sum := sha256.Sum256([]byte(roots.Runtime.Path))
	label := labelPrefix + "." + hex.EncodeToString(sum[:6])
	return Agent{
		Label:      label,
		Definition: filepath.Join(roots.State.Path, "launchd", label+".plist"),
		Program:    program,
		Env:        roots.Overrides(),
		Log:        filepath.Join(roots.State.Path, "logs", "service.log"),
		UID:        os.Getuid(),
		Launchctl:  runLaunchctl,
	}
}

func runLaunchctl(ctx context.Context, args ...string) ([]byte, error) {
	return launchctlCommand(ctx, args...).CombinedOutput()
}

// launchctlCommand is the launchctl call: an absolute path and an allowlisted environment, since
// launchctl needs nothing from the operator's (docs/guides/security.md §4).
func launchctlCommand(ctx context.Context, args ...string) *exec.Cmd {
	cmd := exec.CommandContext(ctx, "/bin/launchctl", args...)
	cmd.Env = []string{"PATH=/usr/bin:/bin:/usr/sbin:/sbin"}
	return cmd
}

func (a Agent) domain() string { return "gui/" + strconv.Itoa(a.UID) }
func (a Agent) target() string { return a.domain() + "/" + a.Label }

// Launch writes the definition and loads it. A job that is still loaded is left as it is:
// neither its definition file nor its process changes, so a definition that differs from this
// build's stays visible through Stale. launchd itself restarts a loaded job that crashed.
func (a Agent) Launch(ctx context.Context) error {
	data, err := a.prepare()
	if err != nil {
		return err
	}
	if _, err := a.Launchctl(ctx, "print", a.target()); err == nil {
		return nil
	}
	if err := a.write(data); err != nil {
		return err
	}
	return a.launchctl(ctx, "bootstrap", a.domain(), a.Definition)
}

// Stale reports whether the definition file differs from the one this build writes. A missing
// file is not stale: nothing is loaded from it. It only reads the file.
func (a Agent) Stale() (bool, error) {
	want, err := a.plist()
	if err != nil {
		return false, err
	}
	got, err := os.ReadFile(a.Definition)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return false, nil
	case err != nil:
		return false, err
	}
	return !bytes.Equal(got, want), nil
}

// Unload removes the job from launchd. A job that is not loaded is left alone.
func (a Agent) Unload(ctx context.Context) error {
	if _, err := a.Launchctl(ctx, "print", a.target()); err != nil {
		return nil
	}
	return a.launchctl(ctx, "bootout", a.target())
}

func (a Agent) launchctl(ctx context.Context, args ...string) error {
	out, err := a.Launchctl(ctx, args...)
	if err == nil {
		return nil
	}
	detail := strings.TrimSpace(string(out))
	if detail == "" {
		detail = err.Error()
	}
	return unavailable(fmt.Sprintf("launchctl %s %s failed: %s", args[0], a.Label, detail), "inspect the job with: launchctl print "+a.target())
}

// prepare refuses a definition that cannot be written, before launchctl is asked anything, and
// returns its content.
func (a Agent) prepare() ([]byte, error) {
	if !filepath.IsAbs(a.Program) {
		return nil, &service.Error{Code: service.CodeInternal, Message: fmt.Sprintf("the ktags executable path %q is not absolute", a.Program), Hint: "run ktags from an absolute path"}
	}
	for _, dir := range []string{filepath.Dir(a.Definition), filepath.Dir(a.Log)} {
		if err := privateDir(dir); err != nil {
			return nil, err
		}
	}
	return a.plist()
}

// write stores the definition with mode 0600 in its private directory, replacing an older one
// in one rename so launchd never reads half a file.
func (a Agent) write(data []byte) error {
	tmp, err := os.CreateTemp(filepath.Dir(a.Definition), ".plist-*")
	if err != nil {
		return unavailable("cannot write the launchd definition "+a.Definition+": "+err.Error(), "check the permissions of the ktags state root")
	}
	defer func() { _ = os.Remove(tmp.Name()) }()
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return unavailable("cannot write the launchd definition "+a.Definition+": "+err.Error(), "check free space in the ktags state root")
	}
	if err := tmp.Close(); err != nil {
		return unavailable("cannot write the launchd definition "+a.Definition+": "+err.Error(), "check free space in the ktags state root")
	}
	if err := os.Rename(tmp.Name(), a.Definition); err != nil {
		return unavailable("cannot replace the launchd definition "+a.Definition+": "+err.Error(), "check the permissions of the ktags state root")
	}
	return nil
}

// privateDir creates dir with mode 0700 and refuses an existing one that others can enter.
func privateDir(dir string) error {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return unavailable("cannot create "+dir+": "+err.Error(), "check the permissions of the ktags state root")
	}
	info, err := os.Stat(dir)
	if err != nil {
		return unavailable("cannot inspect "+dir+": "+err.Error(), "check the permissions of the ktags state root")
	}
	if info.Mode().Perm()&0o077 != 0 {
		return unavailable(fmt.Sprintf("%s has mode %04o; group and others must have no access", dir, info.Mode().Perm()), "chmod 700 "+shell.Quote(dir))
	}
	return nil
}

// unavailable is the error of a launch that the operator's machine refused; the message carries
// the cause, because service.Error wraps only inside its own package.
func unavailable(message, hint string) error {
	return &service.Error{Code: service.CodeUnavailable, Message: message, Hint: hint}
}

// Program returns the executable that the job definition file starts: the first
// ProgramArguments entry. It only reads the file.
func Program(definition string) (string, error) {
	data, err := os.ReadFile(definition) //nolint:gosec // the definition path is derived from the ktags state root
	if err != nil {
		return "", err
	}
	dec := xml.NewDecoder(bytes.NewReader(data))
	arguments := false
	for {
		tok, err := dec.Token()
		if err != nil {
			if errors.Is(err, io.EOF) {
				return "", errors.New("the definition has no ProgramArguments")
			}
			return "", fmt.Errorf("the definition is not a property list: %w", err)
		}
		start, ok := tok.(xml.StartElement)
		if !ok {
			continue
		}
		switch start.Name.Local {
		case "key":
			var key string
			if err := dec.DecodeElement(&key, &start); err != nil {
				return "", fmt.Errorf("the definition is not a property list: %w", err)
			}
			arguments = key == "ProgramArguments"
		case "array":
		case "string":
			if !arguments {
				continue
			}
			var program string
			if err := dec.DecodeElement(&program, &start); err != nil {
				return "", fmt.Errorf("the definition is not a property list: %w", err)
			}
			return program, nil
		default:
			arguments = false
		}
	}
}

func (a Agent) plist() ([]byte, error) {
	var b bytes.Buffer
	str := func(indent, s string) {
		b.WriteString(indent + "<string>")
		_ = xml.EscapeText(&b, []byte(s))
		b.WriteString("</string>\n")
	}
	key := func(indent, s string) {
		b.WriteString(indent + "<key>")
		_ = xml.EscapeText(&b, []byte(s))
		b.WriteString("</key>\n")
	}
	b.WriteString(xml.Header)
	b.WriteString(`<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">` + "\n")
	b.WriteString("<plist version=\"1.0\">\n<dict>\n")
	key("\t", "Label")
	str("\t", a.Label)
	key("\t", "ProgramArguments")
	b.WriteString("\t<array>\n")
	for _, arg := range []string{a.Program, "service", "run"} {
		str("\t\t", arg)
	}
	b.WriteString("\t</array>\n")
	key("\t", "EnvironmentVariables")
	b.WriteString("\t<dict>\n")
	for _, assignment := range a.Env {
		name, value, ok := strings.Cut(assignment, "=")
		if !ok || name == "" {
			return nil, &service.Error{Code: service.CodeInternal, Message: fmt.Sprintf("environment entry %q is not NAME=value", assignment), Hint: "report this as a bug"}
		}
		key("\t\t", name)
		str("\t\t", value)
	}
	b.WriteString("\t</dict>\n")
	key("\t", "RunAtLoad")
	b.WriteString("\t<true/>\n")
	key("\t", "KeepAlive")
	b.WriteString("\t<dict>\n")
	key("\t\t", "SuccessfulExit")
	b.WriteString("\t\t<false/>\n\t</dict>\n")
	key("\t", "StandardOutPath")
	str("\t", a.Log)
	key("\t", "StandardErrorPath")
	str("\t", a.Log)
	b.WriteString("</dict>\n</plist>\n")
	return b.Bytes(), nil
}
