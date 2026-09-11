// Package health runs named, read-only checks against each customer, on demand or at an interval
// while the local service is active (ADR-0001). A health run changes nothing and takes no
// mutation lock.
//
// Every check has a severity and its own timeout. The engine enforces the timeout through the
// injected clock even when a check ignores its context, so one hung customer releases its fleet
// slot and never blocks another customer or the scheduler. At most Options.Concurrency customers
// are measured at once. The schedule is the schedule package, so a slot missed during sleep is
// reported as missed and never run late.
//
// The latest result of each customer is cached with the time it was measured. A View always
// carries that time and whether it is stale. A customer that has never been measured has health
// unknown and is stale: absence is never shown as green. Check details pass the redactor before
// they are kept.
package health
