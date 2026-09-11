package tui

import (
	"context"
	"errors"
	"fmt"
	"time"

	zone "github.com/lrstanley/bubblezone/v2"

	"github.com/nesiler/ktags/internal/service"
)

// Client is the service protocol as the TUI uses it; service.Client implements it.
type Client interface {
	Hello(ctx context.Context) (service.Hello, error)
	Fleet(ctx context.Context) (service.FleetInfo, error)
	Customers(ctx context.Context) ([]service.CustomerInfo, error)
	Actions(ctx context.Context) ([]service.ActionInfo, error)
	Runs(ctx context.Context) ([]service.RunInfo, error)
	Start(ctx context.Context, action string, target service.Target, args map[string]any) (service.RunInfo, error)
	Cancel(ctx context.Context, id string) (service.RunInfo, error)
	Events(ctx context.Context, id string, after uint64, follow bool, fn func(service.Event) error) (service.RunInfo, error)
}

// Options configure a Model.
type Options struct {
	// Client reaches the ktags service. Required.
	Client Client
	// Version is the ktags build shown in the top bar.
	Version string
	// Now is the client clock for data and run ages. Defaults to time.Now.
	Now func() time.Time
	// Poll is the refresh interval. Zero disables polling; Run sets a default.
	Poll time.Duration
	// Zones hit-tests mouse events. Nil creates one; the caller closes a manager it passes.
	Zones *zone.Manager
	// StartErr is why the service could not be started before the TUI opened. The TUI then
	// opens in the service unavailable state with this cause, and Enter retries.
	StartErr error
}

// requestTimeout bounds one request to the service.
const requestTimeout = 10 * time.Second

// problem is an operator-facing error: what failed, the measured cause, the next command.
type problem struct {
	code    string
	message string
	hint    string
}

// describe turns a client error into a problem. A protocol error keeps its code, message and
// hint; anything else is reported as it came.
func describe(err error) problem {
	var se *service.Error
	switch {
	case errors.As(err, &se):
		return problem{code: se.Code, message: se.Message, hint: se.Hint}
	case errors.Is(err, context.DeadlineExceeded):
		return problem{code: service.CodeUnavailable, message: fmt.Sprintf("the ktags service did not answer within %s", requestTimeout), hint: "ktags service status"}
	default:
		return problem{message: err.Error(), hint: "ktags service status"}
	}
}
