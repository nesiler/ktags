// Package connect runs the named connection checks that tell laptop, SSH, Kubernetes and Rancher
// reachability apart. Each check goes through an injectable adapter (SSH, API): the Fake adapters
// for tests, SSHClient and HTTPSAPI for real networks. Both kinds satisfy one contract suite.
//
// Every target has three checks in order: endpoint (DNS, TCP, and for SSH the host key),
// authentication, and minimal authorization (sudo for SSH). A check after a failed or not
// configured one is skipped, never reported green. An adapter classifies a failure with a
// *Failure; a kind that does not belong to the check, or a plain error, is reported as
// unclassified rather than trusted. Every Result states the target, when it was measured, how
// long it stays fresh, a read-only next step and, when it is not ok, a stable runbook id; its
// free text passes the Runner's redactor first (security.md §1).
package connect
