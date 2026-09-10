package run

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/nesiler/ktags/internal/actions"
)

const (
	runsDir = "runs"
	// globalDir holds runs of global actions. The leading underscore cannot start a customer ID.
	globalDir = "_global"

	defaultWindow         = 256
	defaultMaxSubscribers = 16
)

var (
	// customerPattern mirrors the inventory customer ID rule (internal/inventory/validate.go); it
	// also keeps the customer name a single safe path segment.
	customerPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,38}[a-z0-9]$`)
	runIDPattern    = regexp.MustCompile(`^[0-9]{8}T[0-9]{6}Z-[0-9a-f]{8}$`)
)

// Options configure a Store.
type Options struct {
	// Redact masks secret material in free text. It is required: every event step, event message
	// and result summary passes through it before it is written (ADR-0002).
	Redact func(string) string
	// Clock stamps runs, events and results. Defaults to time.Now.
	Clock func() time.Time
	// Random supplies the random part of run IDs. Defaults to crypto/rand.
	Random io.Reader
	// Window is how many recent events a Recorder keeps in memory for live subscribers; older
	// events are read back from events.jsonl. Defaults to 256.
	Window int
	// MaxSubscribers bounds the live subscriptions of one Recorder. Defaults to 16.
	MaxSubscribers int
}

// Store creates and reads run directories below one state root.
type Store struct {
	root           string
	redact         func(string) string
	clock          func() time.Time
	random         io.Reader
	window         int
	maxSubscribers int
	fs             fileSystem
}

// NewStore returns a Store for the state root stateRoot (paths.Roots.State).
func NewStore(stateRoot string, opts Options) (*Store, error) {
	if !filepath.IsAbs(stateRoot) {
		return nil, &Error{Problem: fmt.Sprintf("state root %q is not an absolute path", stateRoot), Next: "resolve the state root with the paths package"}
	}
	if opts.Redact == nil {
		return nil, &Error{Problem: "no redactor configured", Next: "pass the secret mask as Options.Redact; run records never hold unmasked text"}
	}
	s := &Store{
		root:           filepath.Join(filepath.Clean(stateRoot), runsDir),
		redact:         opts.Redact,
		clock:          opts.Clock,
		random:         opts.Random,
		window:         opts.Window,
		maxSubscribers: opts.MaxSubscribers,
		fs:             osFS,
	}
	if s.clock == nil {
		s.clock = time.Now
	}
	if s.random == nil {
		s.random = rand.Reader
	}
	if s.window <= 0 {
		s.window = defaultWindow
	}
	if s.maxSubscribers <= 0 {
		s.maxSubscribers = defaultMaxSubscribers
	}
	return s, nil
}

// Start creates the directory of a new run of action against target, writes its metadata and
// returns the Recorder that appends its events and writes its result.
func (s *Store) Start(ctx context.Context, action string, target actions.Target) (*Recorder, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if strings.TrimSpace(action) == "" {
		return nil, &Error{Problem: "the action ID is empty", Next: "start the run with a registered action ID"}
	}
	group, err := targetDir(target)
	if err != nil {
		return nil, err
	}
	id, err := s.newID()
	if err != nil {
		return nil, err
	}
	parent := filepath.Join(s.root, group)
	if err := os.MkdirAll(parent, dirMode); err != nil {
		return nil, &Error{Run: id, Problem: "cannot create the runs directory", Next: "check the permissions of the state root", Err: err}
	}
	dir := filepath.Join(parent, id)
	if err := os.Mkdir(dir, dirMode); err != nil {
		return nil, &Error{Run: id, Problem: "cannot create the run directory", Next: "check the permissions of the state root, then start the run again", Err: err}
	}
	meta := Meta{
		Version:   FormatVersion,
		ID:        id,
		Action:    action,
		Target:    Target{Kind: target.Kind, Customer: target.Customer, Node: target.Node},
		StartedAt: s.clock().UTC(),
	}
	body, err := json.Marshal(meta)
	if err != nil {
		return nil, &Error{Run: id, Problem: "cannot encode the run metadata", Next: "report this as a bug", Err: err}
	}
	if err := s.fs.writeAtomic(filepath.Join(dir, metaFile), append(body, '\n')); err != nil {
		return nil, &Error{Run: id, Problem: "cannot write " + metaFile, Next: "check free space and permissions, then start the run again", Err: err}
	}
	events, err := os.OpenFile(filepath.Join(dir, eventsFile), os.O_WRONLY|os.O_CREATE|os.O_EXCL|os.O_APPEND, fileMode) //nolint:gosec // dir was just created below the state root
	if err != nil {
		return nil, &Error{Run: id, Problem: "cannot create " + eventsFile, Next: "check free space and permissions, then start the run again", Err: err}
	}
	return &Recorder{
		store:  s,
		dir:    dir,
		meta:   meta,
		events: events,
		subs:   make(map[*Subscription]struct{}),
	}, nil
}

// targetDir is the directory below runs/ that holds the runs of target. Customer and node
// names are checked here because the customer becomes a path segment.
func targetDir(target actions.Target) (string, error) {
	refuse := func(problem string) error {
		return &Error{Problem: problem, Next: "start the run with the target the action registry validated"}
	}
	switch target.Kind {
	case actions.TargetGlobal:
		if target.Customer != "" || target.Node != "" {
			return "", refuse("a global target takes no customer or node")
		}
		return globalDir, nil
	case actions.TargetCustomer, actions.TargetNode:
		if !customerPattern.MatchString(target.Customer) {
			return "", refuse("the customer is not a valid customer ID")
		}
		if (target.Kind == actions.TargetNode) != (target.Node != "") {
			return "", refuse("a node target needs a node, a customer target takes none")
		}
		return target.Customer, nil
	default:
		return "", refuse(fmt.Sprintf("unknown target kind %q", target.Kind))
	}
}

// newID returns a time-ordered run ID such as 20260910T120000Z-1a2b3c4d.
func (s *Store) newID() (string, error) {
	var b [4]byte
	if _, err := io.ReadFull(s.random, b[:]); err != nil {
		return "", &Error{Problem: "cannot generate a run ID", Next: "check the system random source", Err: err}
	}
	return s.clock().UTC().Format("20060102T150405Z") + "-" + hex.EncodeToString(b[:]), nil
}

// List reconstructs every run below the state root, oldest first. A damaged run is listed with
// status incomplete and its problem; it does not hide the others.
func (s *Store) List(ctx context.Context) ([]Run, error) {
	groups, err := os.ReadDir(s.root)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, &Error{Problem: "cannot list the runs directory", Next: "check the permissions of the state root", Err: err}
	}
	var runs []Run
	for _, group := range groups {
		if !group.IsDir() {
			continue
		}
		entries, err := os.ReadDir(filepath.Join(s.root, group.Name()))
		if err != nil {
			return nil, &Error{Run: group.Name(), Problem: "cannot list the runs directory", Next: "check the permissions of the state root", Err: err}
		}
		for _, entry := range entries {
			if !entry.IsDir() || !runIDPattern.MatchString(entry.Name()) {
				continue
			}
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			runs = append(runs, readRun(filepath.Join(s.root, group.Name(), entry.Name())))
		}
	}
	sort.Slice(runs, func(i, j int) bool {
		a, b := filepath.Base(runs[i].Dir), filepath.Base(runs[j].Dir)
		if a != b {
			return a < b
		}
		return runs[i].Dir < runs[j].Dir
	})
	return runs, nil
}

// Load reconstructs the run with the given ID.
func (s *Store) Load(ctx context.Context, id string) (Run, error) {
	if err := ctx.Err(); err != nil {
		return Run{}, err
	}
	dir, err := s.find(id)
	if err != nil {
		return Run{}, err
	}
	return readRun(dir), nil
}

// Events returns the persisted events of run id with an ID above after, for replay from a
// cursor. A torn final line is left out.
func (s *Store) Events(ctx context.Context, id string, after uint64) ([]Event, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	dir, err := s.find(id)
	if err != nil {
		return nil, err
	}
	scan, err := readEvents(filepath.Join(dir, eventsFile), after, 0)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return scan.events, &Error{Run: id, Problem: "events are damaged; the events before the damage are returned", Next: "inspect " + filepath.Join(dir, eventsFile), Err: err}
	}
	return scan.events, nil
}

func (s *Store) find(id string) (string, error) {
	if !runIDPattern.MatchString(id) {
		return "", &Error{Problem: fmt.Sprintf("%q is not a run ID", id), Next: "copy the run ID from the run list"}
	}
	matches, err := filepath.Glob(filepath.Join(s.root, "*", id))
	if err != nil {
		return "", &Error{Run: id, Problem: "cannot search the runs directory", Next: "check the permissions of the state root", Err: err}
	}
	switch len(matches) {
	case 0:
		return "", &Error{Run: id, Problem: "no such run", Next: "list the runs and copy an existing run ID"}
	case 1:
		return matches[0], nil
	default:
		return "", &Error{Run: id, Problem: "the run ID exists below more than one customer", Next: "inspect " + s.root + " and remove the copied run directory"}
	}
}

// readRun reconstructs one run directory. It never fails: what cannot be read becomes the
// run's Problem and, when it affects the outcome, status incomplete.
func readRun(dir string) Run {
	id := filepath.Base(dir)
	run := Run{Dir: dir, Meta: Meta{ID: id}, Status: StatusRunning}
	var problems []string
	damaged := func(problem string) {
		problems = append(problems, problem)
		run.Status = StatusIncomplete
	}

	if data, err := os.ReadFile(filepath.Join(dir, metaFile)); err != nil { //nolint:gosec // dir is below the state root
		damaged(metaFile + " is missing or unreadable")
	} else {
		var meta Meta
		switch err := decodeStrict(data, &meta); {
		case err != nil:
			damaged(metaFile + ": " + err.Error())
		case meta.Version != FormatVersion:
			damaged(fmt.Sprintf("%s: format version %d, want %d", metaFile, meta.Version, FormatVersion))
		case meta.ID != id:
			damaged(metaFile + ": the run ID does not match the directory name")
		default:
			run.Meta = meta
		}
	}

	scan, err := readEvents(filepath.Join(dir, eventsFile), math.MaxUint64, 0)
	switch {
	case errors.Is(err, fs.ErrNotExist):
	case err != nil:
		damaged(err.Error())
	case scan.torn:
		problems = append(problems, fmt.Sprintf("%s ends in a partial line after event %d; it is ignored", eventsFile, scan.last))
	}
	run.LastEventID = scan.last

	data, err := os.ReadFile(filepath.Join(dir, resultFile)) //nolint:gosec // dir is below the state root
	switch {
	case errors.Is(err, fs.ErrNotExist):
	case err != nil:
		damaged(resultFile + " is unreadable; the events before it are kept")
	default:
		var result Result
		switch err := decodeStrict(data, &result); {
		case err != nil:
			damaged(resultFile + " is partial or unreadable (" + err.Error() + "); the events before it are kept")
		case result.Version != FormatVersion:
			damaged(fmt.Sprintf("%s: format version %d, want %d", resultFile, result.Version, FormatVersion))
		case !result.Status.final():
			damaged(fmt.Sprintf("%s: status %q is not a final status", resultFile, result.Status))
		case result.LastEventID > scan.last:
			damaged(fmt.Sprintf("%s names event %d but %s ends at event %d", resultFile, result.LastEventID, eventsFile, scan.last))
		case run.Status == StatusRunning:
			run.Status = result.Status
			run.Result = &result
		}
	}
	run.Problem = strings.Join(problems, "; ")
	return run
}
