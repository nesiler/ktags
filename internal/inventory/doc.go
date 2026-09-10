// Package inventory owns the ktags record of one customer: its identity, its cluster, the
// cluster's nodes and the enabled access methods. The record lives in the customer's Ansible
// inventory directory at group_vars/all/ktags.yml, so playbooks read the same identity as ktags.
// It holds no secret values; a field that needs a credential names a key in the customer's
// secret store. Load decodes strictly and validates; Save validates before it atomically replaces
// the previous file.
package inventory
