// Package schedule runs jobs at a fixed interval and keeps laptop sleep honest (ADR-0001). A slot
// that could not run within its grace window is recorded as missed and is never run late, so a
// sleep is never reported as work that happened. After a sleep, one catch-up run starts at wake
// and the interval grid restarts from there. Time comes from an injected Clock; waits are
// capped so that a wall-clock jump is seen even when timers pause while the machine sleeps.
package schedule
