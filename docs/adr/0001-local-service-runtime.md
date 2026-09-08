# ADR 0001: Local service runtime

## Context

ktags runs from an operator laptop. Health checks should continue while the laptop is awake even
when the TUI is closed, and long actions must survive terminal loss. Reopening ktags must show the
same run and result. tmux is useful for development but is not a reliable product contract.

## Decision

Use one user-level local ktags service. CLI and TUI are clients and communicate through a private,
versioned Unix-socket protocol. The service owns scheduling and action execution. Client exit
detaches from a run; cancellation is an explicit action. On macOS the service is managed through a
user launchd definition. Laptop sleep pauses execution and scheduling; after wake, missed checks are
reported as stale and are not represented as completed.

## Consequences

Actions and automatic health can outlive a terminal but cannot run while the laptop is asleep or
powered off. The protocol and durable run format need compatibility rules. Service stop must handle
active work explicitly. Linux service management can later use systemd user services without
changing the client contract.

## Alternatives considered

- Default tmux session: simple prototype, but couples correctness and lifecycle to a terminal multiplexer.
- In-process CLI/TUI execution: simpler, but terminal loss stops work and prevents reliable reconnect.
- Cluster-resident controller: runs continuously, but adds central/remote infrastructure outside the product boundary.
