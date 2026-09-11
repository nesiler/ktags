package schedule

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"sync"
	"time"
)

const (
	// defaultMaxWait bounds one wait, so a wall-clock jump is noticed within it.
	defaultMaxWait = time.Minute
	// defaultGrace is how late a slot may start and still count as on time, unless the interval
	// is so short that half of it is less.
	defaultGrace = time.Minute
)

// Clock is the time source of a Scheduler.
type Clock interface {
	// Now returns the current wall-clock time.
	Now() time.Time
	// After sends the time once d has elapsed.
	After(d time.Duration) <-chan time.Time
}

type systemClock struct{}

// System is the machine clock. Now carries no monotonic reading: on macOS the monotonic clock
// may stand still while the laptop sleeps, and the schedule must measure the sleep.
func System() Clock { return systemClock{} }

func (systemClock) Now() time.Time                         { return time.Now().Round(0) }
func (systemClock) After(d time.Duration) <-chan time.Time { return time.After(d) }

// Job is one scheduled piece of work.
type Job struct {
	// Name identifies the job in its State; unique within a Scheduler.
	Name string
	// Interval is the time between two slots.
	Interval time.Duration
	// Run does the work of one slot. Its error is recorded in the job's State.
	Run func(ctx context.Context) error
}

// Options tune a Scheduler. Zero values take the defaults.
type Options struct {
	// Grace is how late a slot may start and still run. Defaults to one minute, or half the
	// interval when that is shorter; it must be shorter than every interval.
	Grace time.Duration
	// MaxWait bounds one wait. Defaults to one minute.
	MaxWait time.Duration
}

// State is what a job did and what it will do next.
type State struct {
	Name     string
	Interval time.Duration
	// Next is the next slot.
	Next time.Time
	// Running is set while a run of the job is in progress.
	Running bool
	// Runs counts the runs that ended; LastRun is when the latest of them started and LastErr
	// its error ("" when it succeeded). A missed slot is never counted here.
	Runs    int
	LastRun time.Time
	LastErr string
	// Missed counts the slots that could not run in time; the latest group of them spans
	// MissedFrom to MissedTo.
	Missed     int
	MissedFrom time.Time
	MissedTo   time.Time
	// Stale is set when the latest run is older than one interval plus the grace window, or no
	// run has ended within that time of the first slot.
	Stale bool
}

// Scheduler runs its jobs, each in its own goroutine, so a hung job never delays another.
type Scheduler struct {
	clock   Clock
	grace   map[string]time.Duration
	maxWait time.Duration
	jobs    []Job

	mu     sync.Mutex
	states map[string]*jobState
	wg     sync.WaitGroup
	once   sync.Once
}

type jobState struct {
	State
	// since is the first slot; staleness counts from it until a run has ended.
	since time.Time
}

// New validates the jobs. It refuses an empty or repeated name, an interval that is not
// positive, a missing Run and a grace window as long as an interval.
func New(clock Clock, jobs []Job, opts Options) (*Scheduler, error) {
	if clock == nil {
		return nil, errors.New("schedule: no clock")
	}
	if opts.Grace < 0 || opts.MaxWait < 0 {
		return nil, errors.New("schedule: grace and maximum wait must not be negative")
	}
	s := &Scheduler{clock: clock, grace: map[string]time.Duration{}, maxWait: opts.MaxWait, jobs: jobs, states: map[string]*jobState{}}
	if s.maxWait == 0 {
		s.maxWait = defaultMaxWait
	}
	for _, job := range jobs {
		switch {
		case job.Name == "":
			return nil, errors.New("schedule: a job has no name")
		case s.grace[job.Name] != 0:
			return nil, fmt.Errorf("schedule: job %q is defined twice", job.Name)
		case job.Interval <= 0:
			return nil, fmt.Errorf("schedule: job %q needs a positive interval, got %s", job.Name, job.Interval)
		case job.Run == nil:
			return nil, fmt.Errorf("schedule: job %q has nothing to run", job.Name)
		}
		grace := opts.Grace
		if grace == 0 {
			grace = min(defaultGrace, job.Interval/2)
		}
		if grace <= 0 || grace >= job.Interval {
			return nil, fmt.Errorf("schedule: job %q: the grace window %s must be shorter than the interval %s", job.Name, grace, job.Interval)
		}
		s.grace[job.Name] = grace
	}
	return s, nil
}

// Start runs every job until ctx ends; the first slot of each is now. Start works once.
func (s *Scheduler) Start(ctx context.Context) {
	s.once.Do(func() {
		now := s.clock.Now()
		s.mu.Lock()
		for _, job := range s.jobs {
			s.states[job.Name] = &jobState{State: State{Name: job.Name, Interval: job.Interval, Next: now}, since: now}
		}
		s.mu.Unlock()
		for _, job := range s.jobs {
			s.wg.Add(1)
			go s.loop(ctx, job)
		}
	})
}

// Wait returns when every job goroutine has ended.
func (s *Scheduler) Wait() { s.wg.Wait() }

// States returns the state of every job, sorted by name, with Stale evaluated now.
func (s *Scheduler) States() []State {
	now := s.clock.Now()
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]State, 0, len(s.states))
	for _, st := range s.states {
		state := st.State
		ref := st.since
		if st.Runs > 0 {
			ref = st.LastRun
		}
		state.Stale = now.Sub(ref) > st.Interval+s.grace[st.Name]
		out = append(out, state)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

func (s *Scheduler) loop(ctx context.Context, job Job) {
	defer s.wg.Done()
	grace := s.grace[job.Name]
	for ctx.Err() == nil {
		now := s.clock.Now()
		s.mu.Lock()
		st := s.states[job.Name]
		d := decide(st.Next, now, job.Interval, grace)
		st.Next = d.next
		if d.missed > 0 {
			st.Missed += d.missed
			st.MissedFrom, st.MissedTo = d.missedFrom, d.missedTo
		}
		st.Running = d.run
		s.mu.Unlock()

		if d.run {
			err := job.Run(ctx)
			if ctx.Err() != nil {
				// The run was cut short by the shutdown; it is not a result.
				return
			}
			s.mu.Lock()
			st.Running = false
			st.Runs++
			st.LastRun = now
			st.LastErr = ""
			if err != nil {
				st.LastErr = err.Error()
			}
			s.mu.Unlock()
			continue
		}
		select {
		case <-ctx.Done():
			return
		case <-s.clock.After(min(d.next.Sub(now), s.maxWait)):
		}
	}
}

// decision is what a job does at one moment.
type decision struct {
	run                  bool
	next                 time.Time
	missed               int
	missedFrom, missedTo time.Time
}

// decide applies the policy to the slot next at time now. A slot runs when now is within grace
// of it. Slots whose grace window closed before now are missed, never run. When slots were
// missed, one run starts now: on the grid if the latest slot is still within grace, otherwise
// as a catch-up that restarts the grid at now. grace must be shorter than interval.
func decide(next, now time.Time, interval, grace time.Duration) decision {
	late := now.Sub(next)
	switch {
	case late < 0:
		return decision{next: next}
	case late <= grace:
		return decision{run: true, next: next.Add(interval)}
	}
	missed := int((late - grace + interval - 1) / interval)
	d := decision{
		run:        true,
		missed:     missed,
		missedFrom: next,
		missedTo:   next.Add(time.Duration(missed-1) * interval),
	}
	if slot := next.Add(time.Duration(missed) * interval); !slot.After(now) {
		d.next = slot.Add(interval)
	} else {
		d.next = now.Add(interval)
	}
	return d
}
