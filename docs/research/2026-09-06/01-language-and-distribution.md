# Language and distribution — market research (6 September 2026)

Research performed with live web sources on the date above. Claims carry their source URL.
Context: 3-5 person DevOps team, all on macOS, operating customer RKE2 clusters through Ansible;
secrets encrypted per person; tool runs on each member's laptop; TUI + CLI; ~9k lines of working Go
already exist (cobra).

## 1. Language: Go / Rust / TypeScript / Python

**Startup and size.** Go and Rust both start trivial programs in ~1-5 ms; not measurable. Binary size
favours Rust: <3 MB with musl vs 15 MB+ for Go
([techbytes 2026](https://techbytes.app/posts/go-vs-rust-cli-tools-performance-dx-guide-2026/),
[besterry](https://besterry.com/posts/rust-vs-go-for-cli-tools/)). Compile times reverse the table:
a typical CLI builds clean in 5-15 s in Go, 1-3 min in Rust; even incremental Rust builds take 10-30 s.

**TypeScript (Node/Bun).** February 2026 measurements: Bun (bytecode) 111 ms, Node SEA + code cache
139.7 ms, plain Node SEA 161.3 ms startup; binaries 60-83 MB (Bun) vs 114-117 MB (Node)
([yyx990803/bun-vs-node-sea-startup](https://github.com/yyx990803/bun-vs-node-sea-startup),
[Evan You](https://x.com/evanyou/status/2025397241218040030)). Node SEA is still Stability 1.1,
macOS only arm64 tested in CI ([Node docs](https://nodejs.org/api/single-executable-applications.html)).
Bun `--compile` cross-compiles to 8 targets but needs manual `codesign` on macOS and Bun's own docs
say the binary is "still very large" ([Bun docs](https://bun.com/docs/bundler/executables)).

**Python.** CLI startup 81-154 ms; in Typer >85 % of the time is the `rich` import
([dev.to](https://dev.to/werner_smit/pythons-startup-tax-when-script-startup-time-becomes-the-bottleneck-2np6),
[Typer #744](https://github.com/tiangolo/typer/discussions/744)). Single-file distribution remains weak.

**Ecosystem.** The Kubernetes/RKE2 world is Go: RKE2 ships as a single Go binary
([rancher/rke2](https://github.com/rancher/rke2)); `golang.org/x/crypto/ssh` is mature and maintained
by the Go team ([pkg.go.dev](https://pkg.go.dev/golang.org/x/crypto/ssh)). Bubble Tea v2 shipped
23 February 2026 with 25,000+ dependents
([summary](https://byteiota.com/bubble-tea-v2-10x-faster-terminal-uis-for-go-developers/),
[repo](https://github.com/charmbracelet/bubbletea)).

**Maintenance.** corrode.dev's Go→Rust migration guide explicitly says: stay in Go for Kubernetes
tooling and CLI tools; choose Rust only for latency-critical, data-race-prone hot paths; and "do not
migrate while learning on the side" ([corrode.dev](https://corrode.dev/learn/migration-guides/go-to-rust/)).
Community consensus: team skill and maintenance horizon decide, not tribalism
([futurion](https://futurion.blog/rust-vs-go-for-cli-tools-picking-a-runtime-without-starting-a-tribal-war/),
[rustvsgo](https://rustvsgo.com/use-case/cli-tools/)).

## 2. Homebrew distribution

**2026 change:** GoReleaser's `brews` block (fake formula installing a binary) is deprecated; the
correct path is `homebrew_casks`; existing users migrate via `tap_migrations.json`
([deprecations](https://goreleaser.com/resources/deprecations/),
[homebrew_formulas](https://goreleaser.com/customization/publish/homebrew_formulas/),
[v2.16](https://goreleaser.com/blog/goreleaser-v2.16/)). Homebrew defines a cask as "pre-built and
signed upstream binary" ([Formula Cookbook](https://docs.brew.sh/Formula-Cookbook)).

**Dependencies:** cask DSL supports `depends_on formula: "ansible"` (or an array)
([Cask Cookbook](https://docs.brew.sh/Cask-Cookbook)), so one `brew install <tap>/<tool>` can pull
sops and age.

**Ansible on Homebrew:** formula `ansible` 14.3.1 depends on `python@3.14`
([formulae.brew.sh](https://formulae.brew.sh/formula/ansible)). Homebrew Python upgrades break the
Ansible venv (classic `No module named 'markupsafe'`,
[Luca Berton](https://lucaberton.medium.com/ansible-troubleshooting-error-no-module-named-markupsafe-8c9f45dae5b5));
versioned formulae carry disable dates ([ansible@12](https://formulae.brew.sh/formula/ansible@12)).
Ansible's own install guide does not list Homebrew as a supported method; it recommends pip in a venv
or pipx ([Ansible docs](https://docs.ansible.com/projects/ansible/latest/installation_guide/intro_installation.html)).
`uv tool install --with-executables-from ansible-core,ansible-lint ansible` is a clean alternative
([uv docs](https://docs.astral.sh/uv/concepts/tools/),
[example](https://samedwardes.com/blog/2025-12-29-how-to-install-ansible-with-uv/)).

**npm distribution** (if TS): platform packages via `optionalDependencies` with `os`/`cpu` is the
standard pattern ([MagicBell](https://www.magicbell.com/blog/distributing-platform-specific-binaries-with-npm),
[Sentry](https://sentry.engineering/blog/publishing-binaries-on-npm)). **cargo-dist** is alive:
0.32.0, 21 May 2026 ([releases](https://github.com/axodotdev/cargo-dist/releases)).

## 3. Calling Ansible vs leaving it

- **Subprocess wrapper:** `apenella/go-ansible` v2 still maintained; runs `ansible-playbook` etc.
  in a structured way without re-implementing Ansible ([repo](https://github.com/apenella/go-ansible)).
- **ansible-runner:** Python API, but does not let you control Ansible execution; from Go it adds a
  Python bridge for nothing ([docs](https://docs.ansible.com/projects/runner/en/latest/ansible_runner/)).
- **Embedding (pex/zipapp):** zipapp does not manage dependencies; PEX does but Ansible's C
  extensions (cryptography, libssh) make the package platform-specific and brittle.
- **Leaving Ansible:** pyinfra is pure Python, ~6× faster, truly agentless; small community, "you write
  your own solutions" ([pyinfra.com](https://pyinfra.com/), [Lobsters](https://lobste.rs/s/rrv1hx/why_you_should_try_pyinfra),
  [2025 evaluation](https://jakski.github.io/posts/2025-11-18.html)). Ansible's pains are still
  discussed in 2026 (CfgMgmtCamp 2026; an academic study of 59,157 posts + 20 interviews) but there is
  no mass migration ([CfgMgmtCamp 2026](https://hackmd.io/_mxZmMfmRUS5dvsuKNILCg),
  [arXiv](https://arxiv.org/html/2504.08678v2)).

## 4. Embedding age and sops

- **Go:** `filippo.io/age` v1.3.2 (29 Aug 2026). Passphrase encryption via
  `NewScryptRecipient`/`NewScryptIdentity`, `SetWorkFactor`. A scrypt recipient must be the file's
  only recipient and is "not recommended for automated systems"
  ([pkg.go.dev](https://pkg.go.dev/filippo.io/age)).
- **Rust:** `age` crate 0.11.4 / `rage` 0.12.1 (July 2026), active ([docs.rs](https://docs.rs/age),
  [str4d/rage](https://github.com/str4d/rage/releases)).
- **sops as a library:** only `github.com/getsops/sops/v3/decrypt` has a stability guarantee
  (`Data`, `DataWithFormat`, `File`) ([pkg.go.dev](https://pkg.go.dev/github.com/getsops/sops/v3/decrypt)).
  No stable encrypt/edit API: keep calling the `sops` binary for that.
- sops + age support passphrase-protected identity files (`SOPS_AGE_KEY_FILE`, `SOPS_AGE_KEY_DIR`)
  ([getsops.io](https://getsops.io/docs/usage/identities/age/)).

## 5. Secret storage on a Mac

Simple: age key at `~/.config/sops/age/keys.txt`, mode 0600. Stronger: **age-plugin-se**: the private
key never leaves the Secure Enclave, the on-disk `AGE-PLUGIN-SE-1...` is a handle, Touch ID on every
decrypt ([mko.re](https://mko.re/blog/age-plugin-se/),
[FOSDEM 2025](https://archive.fosdem.org/2025/schedule/event/fosdem-2025-4159-age-plugin-se-building-a-lean-cross-platform-cryptography-tool/),
[brew](https://formulae.brew.sh/formula/age-plugin-se)). **But** sops still does not accept
`age1se1...` recipients: [getsops/sops#1803](https://github.com/getsops/sops/issues/1803) open since
March 2025. Keychain access from Go: `zalando/go-keyring` (uses `/usr/bin/security`, no cgo) and
`99designs/keyring` (cgo Keychain backend) ([zalando](https://github.com/zalando/go-keyring),
[99designs](https://github.com/99designs/keyring)).

## Recommendations

**Language: stay in Go.** The existing 9k lines work; Rust means minutes-long builds and a real
training budget for a 3-5 person team, for a few MB of binary. TS/Bun pays 111 ms startup and 60 MB
for a TUI; Python is out.

**Distribution: own Homebrew tap + GoReleaser `homebrew_casks`**, with `depends_on formula: ["sops", "age"]`.
Do not make Ansible a cask dependency; have the tool detect `ansible-playbook` and manage a pinned
version through `uv tool install` (or pipx), verified by a doctor command.

**Ansible: keep it.** Rewriting hundreds of idempotent steps has no payoff; pyinfra trades YAML for a
small community and self-written modules. Keep `exec` + JSON/callback events; do not add ansible-runner.
Add direct SSH (`x/crypto/ssh`) only for quick diagnostics.

**Secrets:** embed sops decrypt via the stable package, keep encryption in the sops binary; age key in
a 0600 file; Secure Enclave only after sops #1803 closes (or if sops is dropped).
