# ADR 0002: Durable run records

## Context

The local service needs crash-readable action history, streaming progress and reconnect without
introducing a database migration system before the domain model is proven.

## Decision

Store each run in its own state directory with immutable metadata, append-only versioned JSONL
events and one atomically written final result. Event IDs are monotonic within a run and clients
resume from a cursor. Customer audit is a separate append-only stream. Secrets pass through one
redaction boundary before any event, result, error or log is persisted.

## Consequences

The format is easy to inspect and recover after partial writes. Indexes can be rebuilt from files;
fleet-scale history queries may later justify SQLite, which would require a new ADR and migration.

## Alternatives considered

- SQLite now: stronger queries and transactions, with more schema and recovery work before query needs are known.
- Memory only: cannot reconnect or recover results.
- One global JSONL file: simple append path, but awkward isolation, retention and partial-run recovery.
