// Package doctor checks the operator's laptop for what ktags needs and names one fix per
// problem. It is a registry of checks: each component registers its own checks from its own
// file, so a later component adds a check without touching the others. Doctor only reads: it
// never starts the service, never takes a lock and never changes a file.
package doctor
