# ADR 0004: Customer secret store

## Context

Each customer needs one encrypted local store for Rancher credentials, the RKE2 join token,
backup credentials and later secrets. ktags is run by four or five operators, each from their
own laptop, with no shared git and no central server. The local service (ADR-0001) is the only
program that reads or writes a store during normal work. The inventory directory must keep
working with a bare `ansible-playbook -i <dir>` in an emergency (`docs/guides/ansible.md §4`).

The options were sops with age, plain age, or age at rest with sops as an exchange format. sops
supports age plugin identities, including Secure Enclave, since 2025, so hardware-backed
identities do not separate the options.

## Decision

Use **plain age through `filippo.io/age`** inside the service. sops and `community.sops` are not
used.

- **Format.** One age-encrypted, ASCII-armored file per customer at
  `<customer>/group_vars/all/secrets.age`. The plaintext is a flat YAML mapping of the variable
  names playbooks use to their values. Ansible's group_vars loader skips the `.age` extension, so
  the inventory stays valid without ktags.
- **Recipients.** The operators responsible for the customer, as age recipients taken from the
  operator roster (authority defined by the roster decision). The recipient list is kept in plain
  text in the customer's ktags metadata so that it can be checked without decrypting. A write is
  refused when the list is empty or does not contain the writing operator. `ktags doctor` warns
  when a store has fewer than two recipients.
- **Identity discovery.** The operator's identity file lives in the ktags config directory, mode
  `0600` in a `0700` directory; ktags refuses looser permissions. A line may hold a native X25519
  identity or a plugin identity such as `age-plugin-se`; plugin binaries are found on the
  allowlisted `PATH`. ktags never copies, exports or prints an identity.
- **Delivery to Ansible.** The service decrypts into memory and hands the values to
  `ansible-playbook` as an extra-vars file read from a pipe the child inherits
  (`-e @/dev/fd/N`). Never as argument values, environment variables or a temporary file. The
  first task that passes secrets to Ansible proves this with a test.
- **Emergency path.** Without ktags, the `age` CLI decrypts into a process substitution:
  `ansible-playbook -i <dir> <playbook> -e @<(age -d -i <identity> <dir>/group_vars/all/secrets.age)`.
  No plaintext file is written.
- **Re-keying.** A change of responsible operators re-encrypts the store to the new recipient
  list. ktags does this whenever the roster changes a customer's recipients. The audit log records
  who re-keyed and the recipient names, never values.
- **Rotation ownership.** Re-keying does not protect against an operator who already read the
  values. Rotating the values themselves is a separate action, owned by the operator who performs
  a revocation or a scheduled rotation.
- **Recovery.** If every recipient identity is lost, the store cannot be decrypted, by design.
  Mitigations are at least two recipients per customer, an optional team break-glass recipient
  (custody decided with the export policy) and the fact that most values can be re-read or reset
  from the cluster by an operator with SSH access (RKE2 token and kubeconfig from a server node,
  Rancher admin password by reset).
- **Export and import.** Unchanged from `docs/guides/security.md §2`: the bundle is encrypted with
  a passphrase, and import re-keys the store to the importer's recipients.

Hybrid post-quantum recipients, available since age 1.3, are not adopted in v1. Adopting them
later is a re-key, not a format change.

## Consequences

The Homebrew cask depends on `age` only; `age-plugin-se` is optional. There is one fewer binary
and one fewer Ansible collection to pin, and no `.sops.yaml` rules. The ops-platform sops code is
not ported; the `secrets` package is a small new implementation over the age library.

Git history of a customer directory shows that the store changed, not which value changed. The
audit log records secret changes by key name instead.

## Alternatives considered

- sops with age: per-value diffs and in-process decryption through `community.sops` for manual
  runs, at the cost of a second binary, a collection pin and creation rules. Its benefits matter
  most in a shared repository, which ktags does not have.
- age at rest with sops as an exchange format: interoperability with other sops users, at the
  cost of two formats to test and document. No such exchange partner exists.
