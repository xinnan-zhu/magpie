# SSH first-version validation

This record covers the implementation in [SSH management](subsystems/ssh-management.md).
It is development verification, not a claim of compatibility with a real SSH
server, subscription account, or installed Claude Code/Codex version.

## Environment and isolation

- macOS arm64; repository toolchain Go **1.26.8**. An earlier run with the
  machine's Go 1.27.1 was discarded after unrelated dependency differences.
- Playwright 1.62.1, Chromium **147.0.7727.15** (available build 1217) and
  WebKit **26.5**, build 2336. The requested Chromium 1234 download was
  unavailable; no result here claims that newer engine was tested.
- Every Go invocation used temporary HOME/USERPROFILE, XDG config/cache,
  APPDATA/LOCALAPPDATA and pinned Go caches. Package fixtures also isolate
  agent variables. No live agent file, private-key content, real SSH host,
  real supplier API or subscription login was used.
- Transport integration launches an executable fake SSH process and actual
  local TCP/HTTP listeners. Browser tests serve the real assets with local
  API fixtures. Cross-builds and vet do not constitute Windows/Linux runtime
  testing.

## Checks and results

| Check | Result |
| --- | --- |
| `go vet ./...` | Passed on final source |
| `GOOS=linux GOARCH=amd64 go vet -tags nogui ./...` | Passed on final source |
| `GOOS=windows GOARCH=amd64 go vet ./...` | Passed on final source |
| macOS desktop, Linux amd64/arm64 nogui, Windows amd64 builds | Passed |
| `go test -tags nogui -timeout 45m ./...` | Passed; final GUI API/budget guards also rerun after lifecycle changes |
| SSH/access/provider/gateway/GUI concurrency regressions, `-race -count=20` | Passed; provider and transport rerun after the final fixes |
| backup, provider, SSH transport/direct-session, proxy targeted Go tests | Passed |
| Independent filememo, shell-env and model-list regressions, `-race -count=20` | Passed |
| Account-arrange, agent notice language, agent name width, Claude risk prompt reruns | Passed in both engines (62 tests) |
| Built binary: `ssh-control inspect` in an empty isolated HOME | Passed; before/after file lists identical |
| SSH page, host form, model pick, copy preview, tunnel setup and plugin off/on | Passed in Chromium and WebKit; en, zh, zh-TW, ja, de; 1100/420px |
| Navigation, brand, built-in switches, browser gateway mode | Passed after updating SSH-aware fixtures; final lifecycle run also passed gateway mode |
| i18n completeness, duplicate keys and plugin ownership | Passed |
| `make test-ui` (922 files, both engines) | Earlier run stopped before its summary and recorded failures; not a green full-suite result |
| Formatting and `git diff --check` | Passed |

Long checks use `GOMAXPROCS=2` and `-p 2`; browser files use concurrency 1–2.
The earlier browser run initially found the key-name editor double completion,
old navigation fixtures and launch/page timeouts during concurrent builds. The editor
is fixed, navigation fixtures include SSH, and affected files are rerun
separately. Later failures in routing, subscription notices, sync, search and
update checks remain untriaged in that incomplete run. Individual reruns do
not make the unfinished full run green.

### SSH configuration picker follow-up (2026-10-28)

The host form now selects concrete local SSH aliases or accepts a manual
destination and port. Configuration fixtures cover nested/glob/quoted Includes,
cycles, multiple aliases, wildcard exclusion, malformed/unreadable files and
size limits. Discovery never launches SSH or opens an IdentityFile. The desktop
API rejects browser mode and a disabled SSH plugin. Selecting an alias saves
the alias with no port override; saving does not start a connection.

- Passed `go test -race -count=20 -tags nogui ./internal/sshremote ./internal/gui
  -run 'TestSSHConfig|TestSSHRemoteRoutes|TestCoreScriptBudget'` under an
  isolated HOME.
- Passed macOS, Linux amd64 and Windows amd64 vet and builds. The preview
  binary was rebuilt with the final form assets.
- Passed the SSH page plus i18n completeness, ownership, Traditional Chinese,
  Japanese and German checks: 15 top-level tests, Chromium and WebKit,
  five languages and 1100/420px. The page tests exercise selecting, saving,
  editing, switching to manual input, empty/failed config reads and disposal.
- Negative Go overlay ignoring Include directives: `TestSSHConfigHosts`
  failed with missing included aliases; the unchanged implementation passes.
- New full Go and GUI runs are in progress, not green results. Logs are
  `/private/tmp/magpie-ssh-config-suite.log` and
  `/private/tmp/magpie-ssh-config-gui-suite.log`; matching `.exit` files contain
  the exit status when each finishes. The GUI run uses concurrency 1.
- Formatting and `git diff --check` passed. No real SSH host or credentials
  were used; real host policies and agent sessions remain unverified.

The concurrency command is:

```sh
go test -race -count=20 -tags nogui \
  ./internal/access ./internal/provider ./internal/gateway ./internal/sshremote ./internal/gui \
  -run 'TestSSH|TestForwardedWeb|TestManager'
```

Use the isolated-HOME procedure in
[provider plugins](subsystems/provider-plugins.md#verification), not a live HOME.
The page and integration checks can be repeated with:

```sh
node --test --test-concurrency=1 \
  internal/gui/tests/ssh.test.cjs \
  internal/gui/tests/i18n-keys.test.cjs \
  internal/gui/tests/i18n.test.cjs \
  internal/gui/tests/builtins.test.cjs \
  internal/gui/tests/brand.test.cjs \
  internal/gui/tests/sidebar.test.cjs \
  internal/gui/tests/gateway-mode.test.cjs
```

## Failure checks and independent fixes

- Reversing the exact-key guard with a temporary Go overlay makes
  `TestSSHFrontRequiresItsKeyOnLoopbackWithoutLAN` fail with HTTP 401 for
  the correct key. Restoring it passes. The test uses the actual guarded
  server stack with LAN off.
- Disabling `magpie.setActive` in a temporary browser-test copy makes the
  disposal check fail: `switching off releases the connection`. The actual
  kernel invokes existing `onDispose` callbacks when a built-in is disabled;
  its positive test also verifies re-enabling starts without the old snapshot.
- The first full run exposed independent issues. They are separate commits:
  CARGO_HOME missing from test isolation; a filememo channel retained across
  synctest bubbles; NO_PROXY falling through to the system proxy; Escape/blur
  completing key-name editing twice; catalog tests counting the existing
  versioned Codex metadata request as a duplicate plain list request.
- The NO_PROXY regression fails with the old implementation against a fake
  system proxy. The account-arrange test previously reported a second
  `replaceChildren` call; both engines pass after the editor guard.

## Required real-environment checks before claiming compatibility

These steps remain unperformed. Use a disposable remote account and temporary
agent configuration roots, with test credentials chosen explicitly by the
operator. Do not point them at a user's existing agent accounts.

1. Build this version's Linux nogui binary, record its SHA-256, and verify
   the host key/authentication using system OpenSSH in a terminal. Add its
   trusted alias in Remote hosts. Inspect an empty target and verify probing
   does not initialize Magpie or discover agent accounts.
2. Deploy explicitly with `magpie ssh install <host-id> <binary> <sha256>`.
   The GUI's update-feed deployment needs a published release containing
   this SSH protocol; an older official binary cannot provide it. Install
   the user daemon explicitly, or start its existing compatible daemon.
   Verify executable, user, HOME and configuration match the displayed
   serving context, including a shell/service environment that differs.
3. Open remote Web, confirm existing providers are preserved, and exercise
   Claude Code and Codex through their remote Magpie gateway. Check original
   file formats, model picks, drift rejection and disconnect restoration.
4. Start the local-gateway tunnel on a free remote loopback port. Confirm
   missing/wrong credentials fail with LAN off, then make an explicitly
   authorized model request through the managed Remote Magpie provider.
   Exercise the two `magpie ssh-direct` sessions against the installed CLI
   versions and compare agent settings before/after.
5. Close SSH and verify the local key is revoked and forwarded ports close.
   Remote providers/configuration remain. Reconnect, rotate the key, and
   verify selected models remain. Independently configured remote providers
   and the remote daemon must continue after local Magpie quits.
6. Preview and copy selected API providers/groups, first without keys, then
   with explicitly selected test API keys. Check ID remapping, retained remote
   entries and no subscription sign-ins. Change target data between preview
   and apply: the stale plan must be refused. Retry a lost apply reply with
   the same operation: it must return the receipt without duplicates.
7. Exercise unknown/changed host keys, locked keys, jump hosts, denied
   forwarding, occupied ports, GatewayPorts wildcard binding, daemon restart
   under another HOME, an interrupted upload and an interrupted import reply.
   Check displayed errors and recovery without clearing remote configuration.

After a hard local crash, inspect abandoned SSH keys with
`magpie gateway-key list` and revoke the relevant ID with
`magpie gateway-key remove <id>`. An uncatchable upload kill can leave the
managed `deploy.lock` directory: inspect it on the target before removing it.
Deployment/reverse-listener verification is Linux-only; compatible existing
macOS daemons support management/Web, while Windows remote hosts, arbitrary
root/system services and manually started Web processes remain unsupported.
