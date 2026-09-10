// Package actions defines application operations independently of user interfaces.
//
// An action is a module: a Descriptor (identity, typed target and arguments, effect), a
// precondition check and an execution that reports progress and returns a final result. The
// Registry is the single source that clients use to discover and execute actions; it validates
// every definition once, at startup, so a broken action stops the program before any operator
// sees it. Actions are compiled in; there are no dynamic plugins. This package must stay free of
// user-interface and infrastructure (SSH, Kubernetes, Rancher, Ansible) implementations.
package actions
