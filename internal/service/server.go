package service

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"golang.org/x/sys/unix"

	"github.com/nesiler/ktags/internal/actions"
	"github.com/nesiler/ktags/internal/core/domain"
	"github.com/nesiler/ktags/internal/core/fleet"
	"github.com/nesiler/ktags/internal/core/schedule"
	"github.com/nesiler/ktags/internal/run"
)

const (
	socketName = "service.sock"
	lockName   = "service.lock"
	// MaxSocketPath is the longest socket path every supported platform accepts: sun_path is
	// 104 bytes on macOS including the terminating NUL (108 on Linux).
	MaxSocketPath = 103
)

// sysCalls are the system calls Listen makes; tests replace them to fail one step at a time.
type sysCalls struct {
	flock  func(fd, how int) error
	lstat  func(name string) (fs.FileInfo, error)
	remove func(name string) error
	listen func(network string, addr *net.UnixAddr) (*net.UnixListener, error)
	chmod  func(name string, mode os.FileMode) error
}

var sys = sysCalls{flock: unix.Flock, lstat: os.Lstat, remove: os.Remove, listen: net.ListenUnix, chmod: os.Chmod}

// SocketPath is the service socket in the runtime root runtimeDir (paths.Roots.Runtime).
func SocketPath(runtimeDir string) string {
	return filepath.Join(runtimeDir, socketName)
}

// Options configure a Server. Registry, Store and Redact are required.
type Options struct {
	Registry *actions.Registry
	Store    *run.Store
	// DataRoot is the ktags data root holding the customer inventory (paths.Roots.Data).
	DataRoot string
	Build    domain.Build
	// Redact masks secret material in error text sent to clients.
	Redact func(string) string
	// Schedule lists the actions the service runs at an interval (ADR-0001). Every slot that
	// runs is an ordinary run; a slot missed while the laptop slept creates no run.
	Schedule []Scheduled
	// Clock drives the schedule and stamps the fleet summary. Defaults to schedule.System().
	Clock schedule.Clock
	// Fleet hands out the cached health, connection and version facts of the customers. Nil
	// means nothing has been measured: every customer is listed as never measured.
	Fleet fleet.Source
	// Logger is the service log. Nil discards the log.
	Logger *slog.Logger
}

// Scheduled is one action the service runs at an interval.
type Scheduled struct {
	// Name identifies the entry in Server.Schedule.
	Name     string
	Action   string
	Target   Target
	Interval time.Duration
}

// Server is the running service: it owns the socket, the instance lock and every run.
type Server struct {
	build    domain.Build
	redact   func(string) string
	manager  *manager
	listener *net.UnixListener
	lock     *os.File
	sched    *schedule.Scheduler
	clock    schedule.Clock
	fleet    fleet.Source
	// schedStop ends the schedule; stopReq is closed once a client's service.stop is done.
	schedStop context.CancelFunc
	stopReq   chan struct{}
	stopOnce  sync.Once

	// uid is the service owner; peerUID reads a connection's uid. Tests replace both.
	uid     int
	peerUID func(*net.UnixConn) (int, error)
	// accepted and connDone, when set, are called after a connection is accepted and after it
	// is finished. Tests use them.
	accepted func()
	connDone func()

	mu     sync.Mutex
	conns  map[*net.UnixConn]struct{}
	closed bool
	wg     sync.WaitGroup
	once   sync.Once
}

// Listen takes the single-instance lock of runtimeDir and listens on its socket. It refuses a
// runtime root that others can enter, a second service on the same root, and a socket path the
// platform cannot hold.
func Listen(ctx context.Context, runtimeDir string, opts Options) (*Server, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if opts.Registry == nil || opts.Store == nil || opts.Redact == nil {
		return nil, &Error{Code: CodeInternal, Message: "the service needs an action registry, a run store and a redactor", Hint: "report this as a bug"}
	}
	if !filepath.IsAbs(runtimeDir) || !filepath.IsAbs(opts.DataRoot) {
		return nil, &Error{Code: CodeInternal, Message: "the runtime and data roots must be absolute paths", Hint: "resolve the roots with the paths package"}
	}
	socket := SocketPath(runtimeDir)
	if len(socket) > MaxSocketPath {
		return nil, &Error{Code: CodeUnavailable, Message: fmt.Sprintf("the socket path %q is %d bytes; the limit is %d", socket, len(socket), MaxSocketPath), Hint: "set KTAGS_RUNTIME_DIR to a shorter absolute path"}
	}
	info, err := os.Stat(runtimeDir)
	if err != nil {
		return nil, &Error{Code: CodeUnavailable, Message: "cannot inspect the runtime root " + runtimeDir, Hint: "create it with mode 0700, or set KTAGS_RUNTIME_DIR", err: err}
	}
	if !info.IsDir() || info.Mode().Perm()&0o077 != 0 {
		return nil, &Error{Code: CodeUnavailable, Message: fmt.Sprintf("the runtime root %q must be a directory only its owner can enter (mode %04o)", runtimeDir, info.Mode().Perm()), Hint: fmt.Sprintf("chmod 700 %q", runtimeDir)}
	}
	// The schedule is checked before the lock, so a bad entry never takes over the socket.
	var mgr *manager
	sched, err := newSchedule(opts, func() *manager { return mgr })
	if err != nil {
		return nil, err
	}

	lock, err := acquireLock(filepath.Join(runtimeDir, lockName))
	if err != nil {
		return nil, err
	}
	listener, err := listen(socket)
	if err != nil {
		_ = lock.Close()
		return nil, err
	}
	mgr = newManager(opts.Registry, opts.Store, opts.DataRoot, opts.Redact)
	if opts.Logger != nil {
		mgr.log = opts.Logger
	}
	schedCtx, schedStop := context.WithCancel(context.Background())
	clock := opts.Clock
	if clock == nil {
		clock = schedule.System()
	}
	srv := &Server{
		build:     opts.Build,
		redact:    opts.Redact,
		manager:   mgr,
		listener:  listener,
		lock:      lock,
		sched:     sched,
		clock:     clock,
		fleet:     opts.Fleet,
		schedStop: schedStop,
		stopReq:   make(chan struct{}),
		uid:       os.Getuid(),
		peerUID:   peerUID,
		conns:     make(map[*net.UnixConn]struct{}),
	}
	sched.Start(schedCtx)
	return srv, nil
}

// newSchedule turns the scheduled actions into schedule jobs. Every entry is validated against
// the registry, so a schedule never starts an action that run.start would refuse.
func newSchedule(opts Options, mgr func() *manager) (*schedule.Scheduler, error) {
	jobs := make([]schedule.Job, 0, len(opts.Schedule))
	for _, entry := range opts.Schedule {
		target := entry.Target
		if err := opts.Registry.Validate(entry.Action, actions.Request{Target: target.actions()}); err != nil {
			return nil, &Error{Code: CodeInvalid, Message: fmt.Sprintf("scheduled entry %q: %v", entry.Name, err), Hint: "fix the schedule entry, then start the service again", err: err}
		}
		req := Request{Action: entry.Action, Target: &target}
		jobs = append(jobs, schedule.Job{Name: entry.Name, Interval: entry.Interval, Run: func(ctx context.Context) error {
			return mgr().runScheduled(ctx, req)
		}})
	}
	clock := opts.Clock
	if clock == nil {
		clock = schedule.System()
	}
	sched, err := schedule.New(clock, jobs, schedule.Options{})
	if err != nil {
		return nil, &Error{Code: CodeInvalid, Message: err.Error(), Hint: "fix the schedule, then start the service again", err: err}
	}
	return sched, nil
}

// Schedule returns the state of every scheduled action: next slot, runs, missed slots and
// whether its latest result is stale.
func (s *Server) Schedule() []schedule.State {
	return s.sched.States()
}

// StopRequested is closed once a client's service.stop has been carried out: new runs are
// refused and no run is active. The owner of the Server then calls Close.
func (s *Server) StopRequested() <-chan struct{} {
	return s.stopReq
}

// acquireLock takes an exclusive, non-blocking flock on path and writes the service PID into it.
// The kernel releases the lock when the process dies, so a crashed service never blocks the next.
func acquireLock(path string) (*os.File, error) {
	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0o600) //nolint:gosec // path is in the private runtime root
	if err != nil {
		return nil, &Error{Code: CodeUnavailable, Message: "cannot open the service lock " + path, Hint: "check the permissions of the runtime root", err: err}
	}
	if err := sys.flock(int(f.Fd()), unix.LOCK_EX|unix.LOCK_NB); err != nil {
		owner, _ := io.ReadAll(io.LimitReader(f, 32))
		_ = f.Close()
		if errors.Is(err, unix.EWOULDBLOCK) {
			pid := strings.TrimSpace(string(owner))
			if _, perr := strconv.Atoi(pid); perr != nil {
				pid = "unknown"
			}
			return nil, &Error{Code: CodeUnavailable, Message: fmt.Sprintf("another ktags service (pid %s) is already running on %s", pid, filepath.Dir(path)), Hint: "use the running service, or stop it before starting another"}
		}
		return nil, &Error{Code: CodeUnavailable, Message: "cannot lock " + path, Hint: "check that the runtime root is on a local file system", err: err}
	}
	// The PID only improves the refusal message of a second service; the lock itself is what
	// guards the socket, so a failed write does not stop the service.
	_ = f.Truncate(0)
	_, _ = f.WriteAt([]byte(strconv.Itoa(os.Getpid())+"\n"), 0)
	return f, nil
}

// listen replaces a socket left by a dead service and listens with mode 0600. The caller holds
// the instance lock, so the old socket cannot belong to a live service.
func listen(socket string) (*net.UnixListener, error) {
	info, err := sys.lstat(socket)
	switch {
	case errors.Is(err, fs.ErrNotExist):
	case err != nil:
		return nil, &Error{Code: CodeUnavailable, Message: "cannot inspect " + socket, Hint: "check the permissions of the runtime root", err: err}
	case info.Mode()&fs.ModeSocket == 0:
		return nil, &Error{Code: CodeUnavailable, Message: socket + " exists and is not a socket", Hint: "move the file away; ktags does not remove what it did not create"}
	default:
		if err := sys.remove(socket); err != nil {
			return nil, &Error{Code: CodeUnavailable, Message: "cannot remove the stale socket " + socket, Hint: "remove it by hand, then start the service again", err: err}
		}
	}
	listener, err := sys.listen("unix", &net.UnixAddr{Name: socket, Net: "unix"})
	if err != nil {
		return nil, &Error{Code: CodeUnavailable, Message: "cannot listen on " + socket, Hint: "check the permissions of the runtime root", err: err}
	}
	if err := sys.chmod(socket, 0o600); err != nil {
		_ = listener.Close()
		return nil, &Error{Code: CodeUnavailable, Message: "cannot restrict " + socket + " to its owner", Hint: "check the permissions of the runtime root", err: err}
	}
	return listener, nil
}

// Serve accepts connections until Close. It returns nil after Close and the accept error when
// the listener fails otherwise.
func (s *Server) Serve() error {
	for {
		conn, err := s.listener.AcceptUnix()
		if err != nil {
			if s.isClosed() {
				return nil
			}
			return &Error{Code: CodeInternal, Message: "the service stopped accepting connections: " + err.Error(), Hint: "start the ktags service again", err: err}
		}
		if s.accepted != nil {
			s.accepted()
		}
		s.mu.Lock()
		if s.closed {
			s.mu.Unlock()
			_ = conn.Close()
			return nil
		}
		s.conns[conn] = struct{}{}
		s.wg.Add(1)
		s.mu.Unlock()
		go s.handle(conn)
	}
}

func (s *Server) isClosed() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.closed
}

// Close stops the schedule and accepting, disconnects every client, cancels the active runs,
// waits until each has recorded its result and releases the instance lock. An operator's stop
// goes through service.stop first, which refuses while runs are active; Close is the signal
// path (SIGTERM, logout).
func (s *Server) Close() error {
	var err error
	s.once.Do(func() {
		s.schedStop()
		s.mu.Lock()
		s.closed = true
		for conn := range s.conns {
			_ = conn.Close()
		}
		s.mu.Unlock()
		err = s.listener.Close()
		s.sched.Wait()
		s.manager.close()
		s.wg.Wait()
		if lerr := s.lock.Close(); err == nil {
			err = lerr
		}
	})
	return err
}

func (s *Server) handle(conn *net.UnixConn) {
	defer func() {
		_ = conn.Close()
		s.mu.Lock()
		delete(s.conns, conn)
		s.mu.Unlock()
		s.wg.Done()
		if s.connDone != nil {
			s.connDone()
		}
	}()
	enc := json.NewEncoder(conn)
	send := func(resp Response) error {
		resp.Protocol = ProtocolVersion
		return enc.Encode(resp)
	}
	fail := func(err error) {
		_ = send(Response{Error: s.wireError(err)})
	}

	// The owner check comes before any byte of the request is read.
	uid, err := s.peerUID(conn)
	if err != nil {
		fail(&Error{Code: CodeForbidden, Message: "cannot read the credentials of the connecting process", Hint: "connect through the local socket as the user that runs the service"})
		return
	}
	if uid != s.uid {
		fail(&Error{Code: CodeForbidden, Message: fmt.Sprintf("uid %d does not own this ktags service", uid), Hint: "run ktags as the user that started the service"})
		return
	}

	reader := bufio.NewReader(conn)
	line, err := readLine(reader)
	switch {
	case errors.Is(err, errTooLarge):
		fail(&Error{Code: CodeTooLarge, Message: fmt.Sprintf("the request exceeds %d bytes", maxMessage), Hint: "send one request per line"})
		return
	case err != nil:
		fail(&Error{Code: CodeBadRequest, Message: "the connection ended before a complete request line", Hint: "send one JSON request followed by a newline"})
		return
	}
	req, perr := parseRequest(line)
	if perr != nil {
		fail(perr)
		return
	}

	// A client that goes away cancels only its own request, never a run.
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() {
		_, _ = io.Copy(io.Discard, reader)
		cancel()
	}()
	if err := s.dispatch(ctx, req, send); err != nil {
		fail(err)
	}
}

func parseRequest(line []byte) (Request, *Error) {
	version, ok := peerVersion(line)
	if !ok {
		return Request{}, &Error{Code: CodeBadRequest, Message: "the request is not a JSON object with an integer protocol field", Hint: fmt.Sprintf(`send one JSON object per line, for example {"protocol":%d,"op":"hello"}`, ProtocolVersion)}
	}
	if version != ProtocolVersion {
		return Request{}, mismatch(version, "client")
	}
	var req Request
	if err := decodeStrict(line, &req); err != nil {
		return Request{}, &Error{Code: CodeBadRequest, Message: "the request is malformed: " + err.Error(), Hint: fmt.Sprintf("send a request of ktags protocol %d", ProtocolVersion)}
	}
	return req, nil
}

func (s *Server) dispatch(ctx context.Context, req Request, send func(Response) error) error {
	m := s.manager
	switch req.Op {
	case OpHello:
		return send(Response{Hello: &Hello{Service: s.build.Name, Version: s.build.Version, PID: os.Getpid(), ActiveRuns: m.activeCount()}})
	case OpStop:
		ids, err := m.drain(req.CancelRuns)
		if err != nil {
			return err
		}
		// From here the service is stopping whatever happens to this connection.
		defer s.stopOnce.Do(func() { close(s.stopReq) })
		runs := make([]RunInfo, 0, len(ids))
		for _, id := range ids {
			info, err := m.status(ctx, id)
			if err != nil {
				return err
			}
			runs = append(runs, info)
		}
		return send(Response{Runs: runs})
	case OpInventory:
		customers, err := m.customers(ctx)
		if err != nil {
			return err
		}
		return send(Response{Customers: customers})
	case OpFleet:
		info, err := m.fleet(ctx, s.fleet, s.clock.Now())
		if err != nil {
			return err
		}
		return send(Response{Fleet: &info})
	case OpActions:
		return send(Response{Actions: m.actionList()})
	case OpStart:
		info, err := m.start(req)
		if err != nil {
			return err
		}
		return send(Response{Run: &info})
	case OpCancel:
		info, err := m.cancel(ctx, req.Run)
		if err != nil {
			return err
		}
		return send(Response{Run: &info})
	case OpStatus:
		info, err := m.status(ctx, req.Run)
		if err != nil {
			return err
		}
		return send(Response{Run: &info})
	case OpRuns:
		runs, err := m.list(ctx)
		if err != nil {
			return err
		}
		return send(Response{Runs: runs})
	case OpEvents:
		info, err := m.events(ctx, req.Run, req.After, req.Follow, func(e Event) error {
			return send(Response{Event: &e})
		})
		if err != nil {
			return err
		}
		return send(Response{Run: &info})
	default:
		return &Error{Code: CodeBadRequest, Message: fmt.Sprintf("unknown operation %q", req.Op), Hint: "operations: " + strings.Join([]string{OpHello, OpInventory, OpFleet, OpActions, OpStart, OpCancel, OpStatus, OpRuns, OpEvents, OpStop}, ", ")}
	}
}

// wireError turns any error into the protocol error sent to a client. All text passes the
// redactor: an action's error may quote what the action saw.
func (s *Server) wireError(err error) *Error {
	var out Error
	var pe *Error
	var ae *actions.Error
	var re *run.Error
	switch {
	case errors.As(err, &pe):
		out = Error{Code: pe.Code, Message: pe.Message, Hint: pe.Hint}
	case errors.As(err, &ae):
		out = Error{Code: CodeInvalid, Message: ae.Error(), Hint: ae.Hint}
	case errors.As(err, &re):
		out = Error{Code: CodeInternal, Message: "run: " + re.Problem, Hint: re.Next}
	default:
		out = Error{Code: CodeInternal, Message: err.Error()}
	}
	out.Message = s.redact(out.Message)
	out.Hint = s.redact(out.Hint)
	return &out
}
