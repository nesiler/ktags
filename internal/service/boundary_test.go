package service

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// #11-K2: a second service on the same runtime root is refused and leaves the first one serving.
func TestSecondInstanceRefused(t *testing.T) {
	f := newFixture(t, setup{})
	_, err := Listen(context.Background(), f.runtime, f.options())
	pe := wantCode(t, err, CodeUnavailable)
	if !strings.Contains(pe.Message, "already running") || !strings.Contains(pe.Message, fmt.Sprintf("pid %d", os.Getpid())) {
		t.Fatalf("message %q, want the running service and its pid", pe.Message)
	}
	if _, err := f.client.Hello(context.Background()); err != nil {
		t.Fatalf("the first service stopped answering: %v", err)
	}
}

func TestSecondInstanceRefusedWithUnreadableOwner(t *testing.T) {
	f := newFixture(t, setup{})
	if err := os.WriteFile(filepath.Join(f.runtime, lockName), []byte("garbage"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := Listen(context.Background(), f.runtime, f.options())
	if pe := wantCode(t, err, CodeUnavailable); !strings.Contains(pe.Message, "pid unknown") {
		t.Fatalf("message %q, want pid unknown", pe.Message)
	}
}

func TestLockReleasedOnClose(t *testing.T) {
	f := newFixture(t, setup{})
	runtime := mkdir(t, shortDir(t), "rt")
	for i := 0; i < 2; i++ {
		s, err := Listen(context.Background(), runtime, f.options())
		if err != nil {
			t.Fatalf("Listen %d: %v", i+1, err)
		}
		if err := s.Close(); err != nil {
			t.Fatal(err)
		}
	}
}

func TestStaleSocketReplaced(t *testing.T) {
	f := newFixture(t, setup{before: func(runtime string) {
		staleSocket(t, SocketPath(runtime))
	}})
	if _, err := f.client.Hello(context.Background()); err != nil {
		t.Fatalf("Hello after replacing a stale socket: %v", err)
	}
	info, err := os.Stat(SocketPath(f.runtime))
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("socket mode %v %v, want 0600", info, err)
	}
}

func TestListenRefuses(t *testing.T) {
	broken := errors.New("broken on purpose")
	cases := []struct {
		name    string
		prepare func(t *testing.T, runtime *string, opts *Options)
		code    string
		want    string
		// check runs after the refusal with the real system calls back in place.
		check func(t *testing.T, runtime string, opts Options)
	}{
		{name: "no registry", code: CodeInternal, want: "needs an action registry",
			prepare: func(_ *testing.T, _ *string, o *Options) { o.Registry = nil }},
		{name: "no store", code: CodeInternal, want: "needs an action registry",
			prepare: func(_ *testing.T, _ *string, o *Options) { o.Store = nil }},
		{name: "no redactor", code: CodeInternal, want: "needs an action registry",
			prepare: func(_ *testing.T, _ *string, o *Options) { o.Redact = nil }},
		{name: "relative runtime root", code: CodeInternal, want: "absolute",
			prepare: func(_ *testing.T, r *string, _ *Options) { *r = "rt" }},
		{name: "relative data root", code: CodeInternal, want: "absolute",
			prepare: func(_ *testing.T, _ *string, o *Options) { o.DataRoot = "da" }},
		{name: "socket path too long", code: CodeUnavailable, want: "the limit is 103",
			prepare: func(t *testing.T, r *string, _ *Options) { *r = mkdir(t, *r, strings.Repeat("x", 100)) }},
		{name: "runtime root missing", code: CodeUnavailable, want: "cannot inspect the runtime root",
			prepare: func(_ *testing.T, r *string, _ *Options) { *r = filepath.Join(*r, "missing") }},
		{name: "runtime root open to the group", code: CodeUnavailable, want: "only its owner can enter",
			prepare: func(t *testing.T, r *string, _ *Options) { chmod(t, *r, 0o750) }},
		{name: "runtime root is a file", code: CodeUnavailable, want: "only its owner can enter",
			prepare: func(t *testing.T, r *string, _ *Options) {
				*r = filepath.Join(*r, "file")
				if err := os.WriteFile(*r, nil, 0o600); err != nil {
					t.Fatal(err)
				}
			}},
		{name: "lock cannot be opened", code: CodeUnavailable, want: "cannot open the service lock",
			prepare: func(t *testing.T, r *string, _ *Options) { mkdir(t, *r, lockName) }},
		{name: "flock fails", code: CodeUnavailable, want: "cannot lock",
			prepare: func(t *testing.T, _ *string, _ *Options) {
				withSys(t, func(s *sysCalls) { s.flock = func(int, int) error { return broken } })
			}},
		{name: "socket path holds a file", code: CodeUnavailable, want: "is not a socket",
			prepare: func(t *testing.T, r *string, _ *Options) {
				if err := os.WriteFile(SocketPath(*r), []byte("keep"), 0o600); err != nil {
					t.Fatal(err)
				}
			},
			check: func(t *testing.T, runtime string, _ Options) {
				if data, err := os.ReadFile(SocketPath(runtime)); err != nil || string(data) != "keep" {
					t.Fatalf("the file was touched: %q %v", data, err)
				}
			}},
		{name: "socket cannot be inspected", code: CodeUnavailable, want: "cannot inspect {socket}",
			prepare: func(t *testing.T, _ *string, _ *Options) {
				withSys(t, func(s *sysCalls) { s.lstat = func(string) (os.FileInfo, error) { return nil, broken } })
			}},
		{name: "stale socket cannot be removed", code: CodeUnavailable, want: "cannot remove the stale socket",
			prepare: func(t *testing.T, r *string, _ *Options) {
				staleSocket(t, SocketPath(*r))
				withSys(t, func(s *sysCalls) { s.remove = func(string) error { return broken } })
			}},
		{name: "listen fails", code: CodeUnavailable, want: "cannot listen on",
			prepare: func(t *testing.T, _ *string, _ *Options) {
				withSys(t, func(s *sysCalls) {
					s.listen = func(string, *net.UnixAddr) (*net.UnixListener, error) { return nil, broken }
				})
			},
			check: listenAgain},
		{name: "socket cannot be restricted", code: CodeUnavailable, want: "cannot restrict",
			prepare: func(t *testing.T, _ *string, _ *Options) {
				withSys(t, func(s *sysCalls) { s.chmod = func(string, os.FileMode) error { return broken } })
			},
			check: func(t *testing.T, runtime string, opts Options) {
				// Closing the listener removes the unrestricted socket.
				if _, err := os.Lstat(SocketPath(runtime)); !errors.Is(err, os.ErrNotExist) {
					t.Fatalf("the unrestricted socket is still there: %v", err)
				}
				listenAgain(t, runtime, opts)
			}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newFixture(t, setup{})
			runtime, opts := mkdir(t, shortDir(t), "rt"), f.options()
			saved := sys
			tc.prepare(t, &runtime, &opts)
			s, err := Listen(context.Background(), runtime, opts)
			if err == nil {
				_ = s.Close()
				t.Fatal("Listen succeeded, want a refusal")
			}
			sys = saved
			pe := wantCode(t, err, tc.code)
			if want := strings.ReplaceAll(tc.want, "{socket}", SocketPath(runtime)); !strings.Contains(pe.Message, want) {
				t.Fatalf("message %q, want %q", pe.Message, want)
			}
			if tc.check != nil {
				tc.check(t, runtime, opts)
			}
		})
	}
}

// listenAgain proves a failed Listen released the instance lock.
func listenAgain(t *testing.T, runtime string, opts Options) {
	t.Helper()
	s, err := Listen(context.Background(), runtime, opts)
	if err != nil {
		t.Fatalf("Listen after the failure: %v", err)
	}
	_ = s.Close()
}

// #11-K2: a client of another local identity is refused before its request is read.
func TestForeignPeerRefused(t *testing.T) {
	f := newFixture(t, setup{server: func(s *Server) {
		s.peerUID = func(*net.UnixConn) (int, error) { return os.Getuid() + 1, nil }
	}})
	ctx := context.Background()
	_, err := f.client.Hello(ctx)
	if pe := wantCode(t, err, CodeForbidden); !strings.Contains(pe.Message, fmt.Sprintf("uid %d", os.Getuid()+1)) {
		t.Fatalf("message %q, want the foreign uid", pe.Message)
	}
	_, err = f.client.Start(ctx, "fake quick", global, nil)
	wantCode(t, err, CodeForbidden)
	if runs, err := f.store.List(ctx); err != nil || len(runs) != 0 {
		t.Fatalf("a foreign client started runs: %v %v", runs, err)
	}
}

func TestPeerCredentialFailureRefused(t *testing.T) {
	f := newFixture(t, setup{server: func(s *Server) {
		// The uid would pass; only the error must refuse.
		s.peerUID = func(*net.UnixConn) (int, error) { return os.Getuid(), errors.New("no credentials") }
	}})
	_, err := f.client.Hello(context.Background())
	wantCode(t, err, CodeForbidden)
}

// The real credential call reports this process's uid for a connection from this process.
func TestPeerUIDIsOwnUID(t *testing.T) {
	sock := filepath.Join(shortDir(t), "p.sock")
	l, err := net.ListenUnix("unix", &net.UnixAddr{Name: sock, Net: "unix"})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = l.Close() }()
	client, err := net.Dial("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = client.Close() }()
	server, err := l.AcceptUnix()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = server.Close() }()
	uid, err := peerUID(server)
	if err != nil || uid != os.Getuid() {
		t.Fatalf("peerUID = %d, %v; want %d", uid, err, os.Getuid())
	}
}

// #11-K3: a client of another protocol version gets a readable reply that says what to upgrade.
func TestProtocolMismatchFromClient(t *testing.T) {
	cases := []struct {
		name, payload, hint string
	}{
		{"newer client", `{"protocol":2,"op":"hello"}`, "stop the ktags service and start it again"},
		{"older client", `{"protocol":0,"op":"hello"}`, "upgrade ktags"},
		{"newer client with new fields", `{"protocol":7,"op":"fleet.watch","filter":{"env":["prod"]}}`, "stop the ktags service"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newFixture(t, setup{})
			protocol, pe := f.raw(t, tc.payload+"\n")
			if protocol != ProtocolVersion || pe == nil || pe.Code != CodeProtocolMismatch {
				t.Fatalf("reply protocol %d error %+v, want a protocol_mismatch of protocol %d", protocol, pe, ProtocolVersion)
			}
			if !strings.Contains(pe.Hint, tc.hint) {
				t.Fatalf("hint %q, want %q", pe.Hint, tc.hint)
			}
		})
	}
}

func TestBadRequests(t *testing.T) {
	cases := []struct {
		name, payload, code, want string
	}{
		{"not JSON", "not json\n", CodeBadRequest, "integer protocol field"},
		{"no protocol", `{"op":"hello"}` + "\n", CodeBadRequest, "integer protocol field"},
		{"protocol as a string", `{"protocol":"1","op":"hello"}` + "\n", CodeBadRequest, "integer protocol field"},
		{"unknown field", `{"protocol":1,"op":"hello","extra":1}` + "\n", CodeBadRequest, "unknown or mistyped fields"},
		{"two values on one line", `{"protocol":1,"op":"hello"}{"protocol":1}` + "\n", CodeBadRequest, "integer protocol field"},
		{"unknown operation", `{"protocol":1,"op":"dance"}` + "\n", CodeBadRequest, `unknown operation "dance"`},
		{"no newline before the end", `{"protocol":1,"op":"hello"}`, CodeBadRequest, "ended before a complete request line"},
		{"too large", `{"protocol":1,"op":"hello","pad":"` + strings.Repeat("x", maxMessage) + "\"}\n", CodeTooLarge, "exceeds"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newFixture(t, setup{})
			protocol, pe := f.raw(t, tc.payload)
			if protocol != ProtocolVersion || pe == nil || pe.Code != tc.code || !strings.Contains(pe.Message, tc.want) {
				t.Fatalf("reply protocol %d error %+v, want %s saying %q", protocol, pe, tc.code, tc.want)
			}
			if _, err := f.client.Hello(context.Background()); err != nil {
				t.Fatalf("the service stopped serving: %v", err)
			}
		})
	}
}

// #11-K3 from the client side: a reply of another version or shape is a clear error.
func TestClientRefusesUnreadableReplies(t *testing.T) {
	cases := []struct {
		name, reply string
		call        func(Client) error
		code, want  string
	}{
		{"newer service", `{"protocol":2,"hello":{"service":"ktags"}}`, hello, CodeProtocolMismatch, "this ktags is older than the running service"},
		{"older service", `{"protocol":0}`, hello, CodeProtocolMismatch, "stop the ktags service and start it again"},
		{"not JSON", "garbage", hello, CodeMalformedReply, "not a JSON object"},
		{"unknown field", `{"protocol":1,"surprise":true}`, hello, CodeMalformedReply, "malformed"},
		{"no hello", `{"protocol":1}`, hello, CodeMalformedReply, "no hello field"},
		{"no run", `{"protocol":1}`, status, CodeMalformedReply, "no run field"},
		{"no event and no run", `{"protocol":1}`, events, CodeMalformedReply, "neither an event nor the run"},
		{"too large", `{"protocol":1,"pad":"` + strings.Repeat("x", maxMessage) + `"}`, hello, CodeMalformedReply, "size limit"},
		{"closed without a reply", "", hello, CodeUnavailable, "ended before its reply"},
		{"service error", `{"protocol":1,"error":{"code":"invalid","message":"m","hint":"h"}}`, hello, CodeInvalid, "m"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			client := fakeService(t, tc.reply)
			pe := wantCode(t, tc.call(client), tc.code)
			if !strings.Contains(pe.Error(), tc.want) {
				t.Fatalf("error %q, want %q", pe.Error(), tc.want)
			}
		})
	}
}

func TestClientWithoutService(t *testing.T) {
	client := Client{Socket: filepath.Join(shortDir(t), "none.sock")}
	wantCode(t, hello(client), CodeUnavailable)
}

func TestClientCancelBeforeDial(t *testing.T) {
	client := fakeService(t, "-")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := client.Hello(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("Hello: %v, want context.Canceled", err)
	}
}

// Cancelling while the reply is awaited returns the context's error itself, not a connection
// failure: the connection only ended because the caller asked.
func TestClientCancelWhileWaiting(t *testing.T) {
	sock := filepath.Join(shortDir(t), "w.sock")
	l, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = l.Close() }()
	read, held := make(chan struct{}), make(chan struct{})
	defer close(held)
	go func() {
		conn, err := l.Accept()
		if err != nil {
			return
		}
		defer func() { _ = conn.Close() }()
		_, _ = bufio.NewReader(conn).ReadBytes('\n')
		close(read)
		<-held
	}()
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		<-read
		cancel()
	}()
	_, err = Client{Socket: sock}.Hello(ctx)
	var pe *Error
	if !errors.Is(err, context.Canceled) || errors.As(err, &pe) {
		t.Fatalf("Hello: %v (%T), want the bare context.Canceled", err, err)
	}
}

func TestClientRefusesUnencodableArgument(t *testing.T) {
	client := fakeService(t, "")
	_, err := client.Start(context.Background(), "fake quick", global, map[string]any{"note": func() {}})
	wantCode(t, err, CodeInvalid)
}

func hello(c Client) error {
	_, err := c.Hello(context.Background())
	return err
}

func status(c Client) error {
	_, err := c.Status(context.Background(), missingRun)
	return err
}

func events(c Client) error {
	_, err := c.Events(context.Background(), missingRun, 0, false, func(Event) error { return nil })
	return err
}

// fakeService answers every request with reply and a newline; an empty reply closes the
// connection without answering, "-" holds it open without answering.
func fakeService(t *testing.T, reply string) Client {
	t.Helper()
	sock := filepath.Join(shortDir(t), "f.sock")
	l, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	held := make(chan struct{})
	t.Cleanup(func() {
		close(held)
		_ = l.Close()
	})
	go func() {
		for {
			conn, err := l.Accept()
			if err != nil {
				return
			}
			go func() {
				defer func() { _ = conn.Close() }()
				_, _ = bufio.NewReader(conn).ReadBytes('\n')
				switch reply {
				case "":
				case "-":
					<-held
				default:
					_, _ = conn.Write([]byte(reply + "\n"))
				}
			}()
		}
	}()
	return Client{Socket: sock}
}
