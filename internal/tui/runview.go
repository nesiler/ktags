package tui

import (
	"context"
	"fmt"

	tea "charm.land/bubbletea/v2"

	"github.com/nesiler/ktags/internal/service"
)

// maxLines bounds the events a run view holds; the full log stays in the run record.
const maxLines = 500

// runView is one opened run. It keeps the cursor (the last event ID received), so leaving and
// reopening, or a lost connection, resumes the stream without duplicate or missing lines.
type runView struct {
	id   string
	info service.RunInfo
	// lines are the newest events held; dropped counts older ones no longer held.
	lines   []service.Event
	dropped int
	last    uint64
	// gen identifies the stream; messages of a superseded stream are dropped.
	gen    int
	ch     chan streamMsg
	stop   context.CancelFunc
	lost   bool
	notice string
	// offset is how many lines the log is scrolled up from the bottom; 0 follows.
	offset int
}

// streamMsg is one event of a run stream, or its end with the run's state.
type streamMsg struct {
	gen   int
	event *service.Event
	end   *service.RunInfo
	err   error
}

// openRun shows a run. Reopening the run already held resumes from its cursor; another run
// replaces it and streams from the start.
func (m Model) openRun(info service.RunInfo) (Model, tea.Cmd) {
	m.screen, m.overlay = screenRun, overlayNone
	if m.run != nil && m.run.id == info.ID {
		m.run.halt()
		return m, m.stream()
	}
	if m.run != nil {
		m.run.halt()
	}
	m.run = &runView{id: info.ID, info: info}
	return m, m.stream()
}

// leaveRun is Esc in the run view: it detaches; the run continues in the service.
func (m Model) leaveRun() Model {
	r := m.run
	r.halt()
	m.screen = screenFleet
	if r.info.Status == "running" {
		m.status = "left run " + r.id + "; it continues in the ktags service — reopen it from the Runs tab"
	} else {
		m.status = ""
	}
	return m
}

func (r *runView) halt() {
	if r.stop != nil {
		r.stop()
		r.stop = nil
	}
	r.lost = false
}

// stream follows the run from the cursor. The events arrive on a channel that one tea.Cmd at
// a time reads, so Update never blocks.
func (m *Model) stream() tea.Cmd {
	r := m.run
	m.runGen++
	ctx, cancel := context.WithCancel(context.Background())
	ch := make(chan streamMsg, 16)
	r.gen, r.ch, r.stop, r.lost = m.runGen, ch, cancel, false
	client, id, after, gen := m.client, r.id, r.last, r.gen
	return func() tea.Msg {
		go func() {
			defer close(ch)
			info, err := client.Events(ctx, id, after, true, func(e service.Event) error {
				select {
				case ch <- streamMsg{gen: gen, event: &e}:
					return nil
				case <-ctx.Done():
					return ctx.Err()
				}
			})
			if ctx.Err() != nil {
				return
			}
			select {
			case ch <- streamMsg{gen: gen, end: &info, err: err}:
			case <-ctx.Done():
			}
		}()
		return listen(ch)()
	}
}

func listen(ch <-chan streamMsg) tea.Cmd {
	return func() tea.Msg {
		msg, ok := <-ch
		if !ok {
			return nil
		}
		return msg
	}
}

// resume restarts a lost stream from the cursor.
func (m Model) resume() tea.Cmd {
	r := m.run
	r.notice = fmt.Sprintf("reconnected; resumed run %s after event %d", r.id, r.last)
	return m.stream()
}

func (m Model) onStream(msg streamMsg) (tea.Model, tea.Cmd) {
	r := m.run
	if r == nil || msg.gen != r.gen {
		return m, nil
	}
	if msg.event != nil {
		e := *msg.event
		// A replay after a reconnect starts after the cursor; anything at or before it is a
		// duplicate and is dropped.
		if e.ID <= r.last {
			return m, listen(r.ch)
		}
		r.lines = append(r.lines, e)
		r.last = e.ID
		if extra := len(r.lines) - maxLines; extra > 0 {
			r.lines = r.lines[extra:]
			r.dropped += extra
		}
		if r.offset > 0 {
			r.offset = min(r.offset+1, len(r.lines))
		}
		return m, listen(r.ch)
	}
	r.stop = nil
	if msg.err == nil {
		r.info = *msg.end
		for i := range m.runs {
			if m.runs[i].ID == r.id {
				m.runs[i] = r.info
			}
		}
		if r.info.Status == "running" {
			r.lost = true
		}
		return m, nil
	}
	p := describe(msg.err)
	switch {
	case p.code == service.CodeInvalid && r.last > 0:
		// The service refuses the cursor: reload the run from the start and say so.
		r.notice = fmt.Sprintf("the event cursor %d is no longer valid for run %s; reloaded the run from the start", r.last, r.id)
		r.lines, r.dropped, r.last, r.offset = nil, 0, 0, 0
		return m, m.stream()
	case p.code == service.CodeNotFound || p.code == service.CodeInvalid:
		r.notice = p.message + " — next: " + p.hint
	default:
		r.lost = true
		r.notice = fmt.Sprintf("stream of run %s lost after event %d: %s — reconnecting", r.id, r.last, p.message)
	}
	return m, nil
}

// scroll moves the log by delta lines up (positive) or down; rows is the visible height.
func (r *runView) scroll(delta, rows int) {
	top := max(0, len(r.lines)-rows)
	r.offset = max(0, min(top, r.offset+delta))
}
