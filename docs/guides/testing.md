# Testing guide — layers, evidence, traps

Binding for every PR. The workflow contract (`docs/workflow.md §3`) says evidence is mandatory; this
file says what counts as evidence for each kind of change and which false comforts to avoid.

## 1. Layers

| Layer | What it proves | Runs where | Gate |
|---|---|---|---|
| Static | format, vet, lint, tidy modules, `ansible-lint`, playbook syntax | CI + `make check` | must be green |
| Unit (Go) | one package's behaviour with fakes: inventory schema and round-trip, hosts.yml writer, known_hosts editing, lock atomicity, audit line, confirmation logic (fake TTY), dry-run plan, event parsing, result.json parsing, path resolution, action registry invariants | CI, `-race` | must be green |
| Contract | Go and Ansible agree: `result.json` fixtures from real runs parse; `extra_vars.json` keys equal the ones the guard play requires; every playbook has guard + result plays; every registry action maps to a playbook or probe | CI | must be green |
| TUI | `teatest/v2` goldens per screen; model-level tests per key and palette stage; "same action, three entry points, one confirmation" | CI | must be green |
| Ansible check | `--check --diff` against the example inventory with a stub connection where possible; role variable assertions | CI | must be green |
| Lab end to end | real Ubuntu VMs (Hetzner, `purpose=ktags-test` label): access → preflight → install → node add → health → backup → second install `changed=0` → decommission | maintainer runs, output pasted into the PR | required for `live` tasks |

There is no coverage percentage floor. Instead: every risk named in the PR's *Risk → test* table has
a test, and every guard has break-see-red evidence.

## 2. Evidence standard

- **Claim = evidence.** `file:line`, a command with its output, or a test name. "Works" is not
  evidence; "I ran it" without output is not evidence.
- **Live, this run.** Paste the `make check` output from the final commit, not from an earlier
  one. If you re-committed, re-run.
- **Green is not proof.** For each new guard, validator or safety check: break the guarded
  behaviour, watch the test fail, restore, watch it pass. Record it in one line in the PR
  (`broke X in file:line → TestY red → restored → green`).
- **Negative tests are mandatory** for anything that refuses: the wrong customer name, a missing
  pin, a locked customer, a secret in a log line, a bad exit code.
- **Not done and why** lists every criterion not met with the reason (decision, environment,
  moved to issue #M — and #M exists and says so).

## 3. Fake-green catalogue (things that pass and prove nothing)

- A test that asserts on its own fixture (`want := got`).
- A golden updated with `-update` in the same PR that changed the behaviour, without a reviewed
  diff.
- A mocked subprocess whose fake output was written by the same person in the same hour as the
  parser; pair it with one fixture captured from a real run.
- `t.Skip` behind a tool check (`if _, err := exec.LookPath("age")`) that silently skips in CI:
  CI must install the tool or the test must fail loudly with the missing tool named.
- Table-driven tests where every case takes the happy path.
- `--check` runs declared "green" when the play had `check_mode: false` tasks that ran for real.
- A Hetzner run on a VM that already had a previous install (`changed=0` for the wrong reason).
- Asserting only the exit code of a command that prints the error and continues.

## 4. Fake-red catalogue (things that fail for the wrong reason)

- Goldens rendered at a different width or with colour on: pin `WithInitialTermSize(100, 30)`
  and `NO_COLOR`/ASCII profile in tests.
- Wall-clock time in output (relative ages): inject the clock.
- Map iteration order in rendered tables: sort before rendering.
- Tests depending on `$HOME`, locale or the developer's `~/.config/ktags`: always set `KTAGS_HOME`
  to `t.TempDir()`.
- Race detector flakes from goroutines the test does not wait for: use `t.Cleanup` and channels,
  no `time.Sleep`.

## 5. Writing tests

- Tests next to the code; `testdata/` for fixtures and goldens; fixtures from real runs are named
  with their date and source (`result-health-2026-09-04-lab.json`).
- One assertion of behaviour per test name; prefer `got/want` diffs over booleans.
- No network in unit tests. SSH and Ansible are behind interfaces with fakes; the real
  implementations are exercised only in the lab layer.
- Secrets in fixtures are obviously fake (`age1test…`, `tskey-example`); the guard grep for
  real-looking tokens runs on `testdata/` too.
- TUI: model tests drive `Update` with synthetic `tea.KeyPressMsg`/`tea.MouseClickMsg` and assert
  on model fields; goldens only for the frame layout.

## 6. Lab discipline (real machines)

- Only the maintainer runs commands against real machines. The agent writes the commands into
  the PR under **Acceptance run**; the maintainer pastes the output.
- Lab resources carry the label `purpose=ktags-test` and are created by the lab script; nothing
  else is ever touched. `hermes-k3s` and any customer machine are out of bounds, always.
- Every lab run leaves logs and `result.json` copies under `docs/test-reports/<date>/` with real
  data redacted (IPs are fine for lab VMs; keys, tokens and passwords never).
- A `live` task is not done until the lab run is pasted and green; CI green alone is not enough.

## 7. Checklist before asking for review

1. `make check` green, output pasted.
2. Every criterion K1..Kn has a row with evidence or a not-done reason.
3. Every risk in the plan has a named test.
4. New guard → break-see-red line.
5. No new `t.Skip`, no golden updated without a diff explanation.
6. `live` → acceptance commands written for the maintainer.
