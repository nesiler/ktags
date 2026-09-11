// Package probe composes the connection checks of package connect into health checks: one
// health check per connection check, measured against every node (SSH) or endpoint (Kubernetes,
// Rancher) of a customer, with the target read fresh from the customer's inventory on each run.
// A failure names the node, the measured fact, the next step and a stable runbook id. The probe
// also keeps each customer's connection state: reachable when any measured endpoint answered,
// unreachable only when every node's SSH endpoint and the Kubernetes API failed (#65 D4b).
package probe
