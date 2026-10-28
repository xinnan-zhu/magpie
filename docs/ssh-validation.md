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

This table records the initial implementation checks. The dated follow-ups
below record later changes and their remaining failures; an earlier pass is
not a green full-suite result for the current branch.

| Check | Result |
| --- | --- |
| `go vet ./...` | Passed |
| `GOOS=linux GOARCH=amd64 go vet -tags nogui ./...` | Passed |
| `GOOS=windows GOARCH=amd64 go vet ./...` | Passed |
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
- That full Go run was superseded after installation development changed
  its discovered test-file set mid-run (`fakeInstallation` was not included
  in its earlier file list). It is not a final-source result. The GUI run
  was stopped with exit status 2 after recorded failures. Logs are
  `/private/tmp/magpie-ssh-config-suite.log` and
  `/private/tmp/magpie-ssh-config-gui-suite.log`; matching `.exit` files contain
  the exit status. The GUI run used concurrency 1.
- Formatting and `git diff --check` passed. No real SSH host or credentials
  were used; real host policies and agent sessions remain unverified.

### One-click installation follow-up (2026-10-28)

A missing Linux host now offers **Install and start Magpie**. The existing
verified update download and SSH upload are followed by protocol/context
inspection, the original user daemon installer and authenticated Web readiness.
Existing services are refused, failed mutations are not retried, and the page
refreshes status after failure for service-only recovery. The remote upload
checks the candidate's help before replacing an installation and refuses an
installation that appeared since an absent-host probe.

- Passed the actual desktop deploy API using a local update feed and a fake
  SSH executable, including checksum failure with no upload/service write.
- Passed final `-race -count=20` installation, upload and API regressions in
  `internal/sshremote` and `internal/gui`. The remote upload script itself runs
  against executable shell fixtures; ELF architecture checks run separately.
  No Linux executable is claimed to have run on macOS.
- Passed the SSH UI and i18n checks: 6 top-level tests, both engines, five
  languages, 1100/420px. Cases cover one-click completion, post-failure status
  refresh, no automatic mutation retry and service-only recovery.
- Removing the upload compatibility check, existing-service check and Web
  readiness wait with a Go overlay makes their tests fail with an accepted
  older release, an unexpected existing-service install and a not-ready result.
- The host-save race found during this work is a separate commit: a held
  list read fails the old page with `finish the host refresh before returning
  to row actions`. Its independent base-plus-fix asset copy passes both
  engines, all five languages and 1100/420px.
- Passed macOS/Linux amd64/Windows amd64 vet; macOS desktop, Linux amd64/arm64
  static nogui and Windows amd64 builds. The preview launcher has both locally
  built Linux artifacts behind the existing `MAGPIE_UPDATE_FEED` test hook;
  its local HTTP download/hash check passed. These artifacts are not published.
  This avoids relying on a public release that does not contain SSH yet.
- The final-source full Go run completed with exit status 1 in
  `/private/tmp/magpie-ssh-install-suite.log` (matching `.exit`). All packages
  except `internal/gateway` passed. That package failed
  `TestClientGoneRecordedAsCanceled`, `TestUnroutedSlowBodyNotCutOff` and
  `TestUnroutedMaskingNotCutOff`;
  the isolated `-race -count=20` investigation exited 1 in
  `/private/tmp/magpie-ssh-gateway-investigate.log`. It found a real request
  header race: `unroutedHold.showLocked` reads through `callerOf` while
  `decodedBody` deletes a header. The three original full-run failures and
  this race remain unresolved; this branch is not ready to ship or submit
  as fully verified. Reproduce the investigation with
  `go test -race -count=20 -tags nogui ./internal/gateway -run
  '^(TestClientGoneRecordedAsCanceled|TestUnroutedSlowBodyNotCutOff|TestUnroutedMaskingNotCutOff)$'`
  under the isolated-HOME procedure below.
  The full GUI suite is not green: it was stopped with exit status 2 after
  WebKit zh-TW agent-label/settings failures, alongside the older incomplete
  run's untriaged failures. Focused passes do not make that suite green.
  No real SSH host, real systemd session,
  private-key content or live agent/subscription was used.

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

### Page layout follow-up (2026-10-28)

The page now follows Providers and Agents: every saved host is a row of one
list (no host picker), ＋ Add SSH host sits under it, a host's state is a dot
and a line under its name with the one fixing click on its row, and Edit,
Remove, Reconnect, Disconnect and the remote environment are in the row's
"…" menu. A ready host's agents are read on connect and use the Agents page's
switch; switching off opens the disconnect preview. Dialogs foot Cancel and
the primary action at the right.

- Passed `ssh.test.cjs` and `i18n-keys.test.cjs` in Chromium and WebKit, five
  languages, 1100/420px; also `gui-zh-tw`, `gui-ja`, `gui-de`, `builtins`,
  `sidebar`, `brand`, `gateway-mode`, `ux-safety`, `page-width`,
  `click-scroll`, `settings-lines-wrap` and `user-select`.
- Making the switch disconnect without its preview fails `ssh.test.cjs`
  waiting for the preview's file path; restored, it passes.
- A macOS desktop build with these assets was made for the preview launcher.
  The page was compared with Providers, Agents and the provider editor in
  screenshots of the fixtures, light and dark; no real host was connected.
  `i18n.test.cjs`, named in the commands above, does not exist in the tree.

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
2. On an absent Linux installation, use **Install and start Magpie** and
   verify it reaches the ready state with one click. The GUI's update-feed
   deployment needs an artifact containing this SSH protocol; an older
   official binary cannot provide it. For an offline check, deploy explicitly
   with `magpie ssh install <host-id> <binary> <sha256>` and install the user
   daemon separately. For an existing compatible daemon, start that service.
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
