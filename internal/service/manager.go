package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"sort"
	"strings"
	"sync"

	"github.com/nesiler/ktags/internal/actions"
	"github.com/nesiler/ktags/internal/inventory"
	"github.com/nesiler/ktags/internal/run"
)

// Cancellation causes, recorded as the summary of a cancelled run.
var (
	errCancelled = errors.New("cancelled by the operator")
	errStopping  = errors.New("cancelled because the ktags service stopped")
	errStopRun   = errors.New("cancelled because the operator stopped the ktags service")
)

// manager owns the runs. Runs execute on the manager's context, never on a connection's, so a
// client that disconnects only detaches.
type manager struct {
	registry *actions.Registry
	store    *run.Store
	dataRoot string
	redact   func(string) string

	ctx  context.Context
	stop context.CancelFunc
	wg   sync.WaitGroup

	mu     sync.Mutex
	active map[string]*activeRun
	closed bool
}

type activeRun struct {
	rec    *run.Recorder
	cancel context.CancelCauseFunc
	// done is closed after the result is written (or failed to be) and the run left active.
	done chan struct{}
	// finishErr is the error of writing the result; it is read only after done is closed.
	finishErr error
}

func newManager(registry *actions.Registry, store *run.Store, dataRoot string, redact func(string) string) *manager {
	ctx, stop := context.WithCancel(context.Background())
	return &manager{
		registry: registry,
		store:    store,
		dataRoot: dataRoot,
		redact:   redact,
		ctx:      ctx,
		stop:     stop,
		active:   make(map[string]*activeRun),
	}
}

// close cancels every active run and waits until each has written its result.
func (m *manager) close() {
	m.mu.Lock()
	m.closed = true
	for _, a := range m.active {
		a.cancel(errStopping)
	}
	m.mu.Unlock()
	m.wg.Wait()
	m.stop()
}

func (m *manager) start(req Request) (RunInfo, error) {
	info, _, err := m.startRun(req)
	return info, err
}

// startRun starts a run and also returns its handle, so the scheduler can wait for its end.
func (m *manager) startRun(req Request) (RunInfo, *activeRun, error) {
	if req.Target == nil {
		return RunInfo{}, nil, &Error{Code: CodeInvalid, Message: "run.start needs a target", Hint: `send "target":{"kind":"global"} or a customer target`}
	}
	target := req.Target.actions()
	args, err := m.decodeArgs(req.Action, req.Args)
	if err != nil {
		return RunInfo{}, nil, err
	}
	actionReq := actions.Request{Target: target, Args: args}
	// Refused requests never create a run directory.
	if err := m.registry.Validate(req.Action, actionReq); err != nil {
		return RunInfo{}, nil, err
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return RunInfo{}, nil, &Error{Code: CodeUnavailable, Message: "the ktags service is stopping", Hint: "start the ktags service again, then retry"}
	}
	rec, err := m.store.Start(m.ctx, req.Action, target)
	if err != nil {
		return RunInfo{}, nil, err
	}
	runCtx, cancel := context.WithCancelCause(m.ctx)
	a := &activeRun{rec: rec, cancel: cancel, done: make(chan struct{})}
	m.active[rec.Meta().ID] = a
	m.wg.Add(1)
	go m.execute(runCtx, a, req.Action, actionReq)
	return runInfo(run.Run{Meta: rec.Meta(), Status: run.StatusRunning}), a, nil
}

// runScheduled starts one scheduled run and waits for its end. A run that did not succeed is an
// error, so the schedule records it.
func (m *manager) runScheduled(ctx context.Context, req Request) error {
	info, a, err := m.startRun(req)
	if err != nil {
		return err
	}
	select {
	case <-a.done:
	case <-ctx.Done():
		// The schedule is stopping; the run itself is cancelled by the manager's close.
		return ctx.Err()
	}
	// A result that could not be written leaves the run unfinished, which is not a success.
	final, err := m.status(ctx, info.ID)
	if err != nil {
		return err
	}
	if final.Status != string(run.StatusSucceeded) {
		summary := final.Status
		if final.Result != nil {
			summary += ": " + final.Result.Summary
		}
		return fmt.Errorf("run %s %s", info.ID, summary)
	}
	return nil
}

func (m *manager) activeCount() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.active)
}

// drain prepares an operator's stop. With active runs and without cancelRuns it refuses and
// changes nothing. Otherwise it refuses new runs, cancels the active ones and waits until each
// has written its result. The check and the refusal of new runs are one critical section, so a
// run cannot start between them. It returns the IDs of the cancelled runs, sorted.
func (m *manager) drain(cancelRuns bool) ([]string, error) {
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return nil, &Error{Code: CodeUnavailable, Message: "the ktags service is already stopping", Hint: "wait until it has stopped, then check with the service status"}
	}
	ids := make([]string, 0, len(m.active))
	for id := range m.active {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	if len(ids) > 0 && !cancelRuns {
		m.mu.Unlock()
		return nil, &Error{
			Code:    CodeConflict,
			Message: fmt.Sprintf("the ktags service has %d active run(s): %s", len(ids), strings.Join(ids, ", ")),
			Hint:    "wait for the runs to end, or stop with --cancel-runs to cancel them",
		}
	}
	m.closed = true
	done := make([]chan struct{}, 0, len(ids))
	for _, id := range ids {
		a := m.active[id]
		a.cancel(errStopRun)
		done = append(done, a.done)
	}
	m.mu.Unlock()
	// The wait is not bound to the requesting connection: once decided, the stop completes
	// even if that client goes away.
	for _, ch := range done {
		<-ch
	}
	return ids, nil
}

// decodeArgs converts wire arguments to the Go kinds the action declares. Names the action does
// not declare are passed on, so Validate reports them as unknown.
func (m *manager) decodeArgs(actionID string, raw map[string]json.RawMessage) (map[string]any, error) {
	if len(raw) == 0 {
		return nil, nil
	}
	kinds := map[string]actions.ArgKind{}
	if action, ok := m.registry.Lookup(actionID); ok {
		for _, arg := range action.Descriptor().Args {
			kinds[arg.Name] = arg.Kind
		}
	}
	args := make(map[string]any, len(raw))
	for name, value := range raw {
		var err error
		switch kinds[name] {
		case actions.ArgString:
			var s string
			err = json.Unmarshal(value, &s)
			args[name] = s
		case actions.ArgBool:
			var b bool
			err = json.Unmarshal(value, &b)
			args[name] = b
		case actions.ArgInt:
			var n int
			err = json.Unmarshal(value, &n)
			args[name] = n
		default:
			args[name] = nil
		}
		if err != nil {
			return nil, &Error{Code: CodeInvalid, Message: fmt.Sprintf("argument %q must be a JSON %s", name, kinds[name]), Hint: "list the actions to see each argument's kind"}
		}
	}
	return args, nil
}

func (m *manager) execute(ctx context.Context, a *activeRun, actionID string, req actions.Request) {
	defer m.wg.Done()
	result, err := m.registry.Execute(ctx, actionID, req, a.rec)
	status, summary := outcome(ctx, result, err)
	_, a.finishErr = a.rec.Finish(status, summary)
	m.mu.Lock()
	delete(m.active, a.rec.Meta().ID)
	m.mu.Unlock()
	a.cancel(nil)
	close(a.done)
}

// outcome maps what Execute returned to the recorded status. A successful result stays
// successful even when a cancellation raced it: the work was done.
func outcome(ctx context.Context, result actions.Result, err error) (run.Status, string) {
	switch {
	case err != nil && ctx.Err() != nil:
		return run.StatusCancelled, context.Cause(ctx).Error()
	case err != nil:
		return run.StatusFailed, err.Error()
	case result.Status == actions.StatusSucceeded:
		return run.StatusSucceeded, result.Summary
	default:
		return run.StatusFailed, result.Summary
	}
}

func (m *manager) cancel(ctx context.Context, id string) (RunInfo, error) {
	m.mu.Lock()
	a := m.active[id]
	if a != nil {
		a.cancel(errCancelled)
	}
	m.mu.Unlock()
	info, err := m.status(ctx, id)
	if err != nil || a != nil {
		return info, err
	}
	return RunInfo{}, &Error{Code: CodeConflict, Message: fmt.Sprintf("run %s is %s and not active in this service", id, info.Status), Hint: "check the run with run.status; only an active run can be cancelled"}
}

func (m *manager) status(ctx context.Context, id string) (RunInfo, error) {
	r, err := m.store.Load(ctx, id)
	if err != nil {
		return RunInfo{}, notFound(err)
	}
	return runInfo(r), nil
}

func (m *manager) list(ctx context.Context) ([]RunInfo, error) {
	runs, err := m.store.List(ctx)
	if err != nil {
		return nil, err
	}
	infos := make([]RunInfo, 0, len(runs))
	for _, r := range runs {
		infos = append(infos, runInfo(r))
	}
	return infos, nil
}

// events sends the events of run id after the cursor. With follow it waits for new events of an
// active run until the run ends; the returned RunInfo is then the final state.
func (m *manager) events(ctx context.Context, id string, after uint64, follow bool, send func(Event) error) (RunInfo, error) {
	info, err := m.status(ctx, id)
	if err != nil {
		return RunInfo{}, err
	}
	if after > info.LastEventID {
		return RunInfo{}, &Error{Code: CodeInvalid, Message: fmt.Sprintf("cursor %d is beyond the last event %d of run %s", after, info.LastEventID, id), Hint: "resume from the last event ID the client received"}
	}
	m.mu.Lock()
	a := m.active[id]
	m.mu.Unlock()
	if !follow || a == nil {
		return m.replay(ctx, id, after, send)
	}

	sub, err := a.rec.Subscribe(after)
	if err != nil {
		return RunInfo{}, &Error{Code: CodeConflict, Message: err.Error(), Hint: "close an idle client, or replay without follow", err: err}
	}
	defer sub.Close()
	for {
		event, err := sub.Next(ctx)
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return RunInfo{}, err
		}
		if err := send(wireEvent(event)); err != nil {
			return RunInfo{}, err
		}
	}
	// Wait for done before reading finishErr: done publishes it. The recorder already writes the
	// result before a subscriber sees the end of the events, so the wait is also defence in depth
	// against a change to the recorder's locking.
	select {
	case <-a.done:
	case <-ctx.Done():
		return RunInfo{}, ctx.Err()
	}
	if a.finishErr != nil {
		return RunInfo{}, &Error{Code: CodeInternal, Message: "the run ended but its result could not be written: " + a.finishErr.Error(), Hint: "check free space and permissions of the ktags state root", err: a.finishErr}
	}
	return m.status(ctx, id)
}

func (m *manager) replay(ctx context.Context, id string, after uint64, send func(Event) error) (RunInfo, error) {
	events, err := m.store.Events(ctx, id, after)
	if err != nil {
		return RunInfo{}, err
	}
	for _, event := range events {
		if err := send(wireEvent(event)); err != nil {
			return RunInfo{}, err
		}
	}
	return m.status(ctx, id)
}

// customers lists the inventory. A refused record is listed with its problem and does not hide
// the others.
func (m *manager) customers(ctx context.Context) ([]CustomerInfo, error) {
	root := inventory.CustomersDir(m.dataRoot)
	entries, err := os.ReadDir(root)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, &Error{Code: CodeInternal, Message: "cannot list the customers directory " + root, Hint: "check the permissions of the ktags data root", err: err}
	}
	var infos []CustomerInfo
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		record, err := inventory.Load(ctx, inventory.CustomerDir(m.dataRoot, entry.Name()))
		if err != nil {
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			infos = append(infos, CustomerInfo{ID: entry.Name(), Problem: m.redact(err.Error())})
			continue
		}
		infos = append(infos, CustomerInfo{
			ID:          record.Customer.ID,
			Name:        record.Customer.Name,
			Environment: string(record.Customer.Environment),
			Cluster:     record.Cluster.Name,
			Nodes:       len(record.Cluster.Nodes),
		})
	}
	sort.Slice(infos, func(i, j int) bool { return infos[i].ID < infos[j].ID })
	return infos, nil
}

func (m *manager) actionList() []ActionInfo {
	descriptors := m.registry.List()
	infos := make([]ActionInfo, 0, len(descriptors))
	for _, d := range descriptors {
		info := ActionInfo{ID: d.ID, Title: d.Title, Help: d.Help, Target: string(d.Target), Effect: string(d.Effect)}
		for _, arg := range d.Args {
			info.Args = append(info.Args, ArgInfo{Name: arg.Name, Kind: string(arg.Kind), Required: arg.Required, Help: arg.Help})
		}
		infos = append(infos, info)
	}
	return infos
}

func notFound(err error) error {
	var re *run.Error
	if errors.As(err, &re) {
		return &Error{Code: CodeNotFound, Message: "run: " + re.Problem, Hint: re.Next, err: err}
	}
	return err
}

func runInfo(r run.Run) RunInfo {
	info := RunInfo{
		ID:          r.Meta.ID,
		Action:      r.Meta.Action,
		Target:      Target{Kind: string(r.Meta.Target.Kind), Customer: r.Meta.Target.Customer, Node: r.Meta.Target.Node},
		Status:      string(r.Status),
		StartedAt:   r.Meta.StartedAt,
		LastEventID: r.LastEventID,
		Problem:     r.Problem,
	}
	if r.Result != nil {
		info.Result = &ResultInfo{Status: string(r.Result.Status), Summary: r.Result.Summary, FinishedAt: r.Result.FinishedAt}
	}
	return info
}

func wireEvent(e run.Event) Event {
	return Event{ID: e.ID, Time: e.Time, Step: e.Step, Message: e.Message}
}
