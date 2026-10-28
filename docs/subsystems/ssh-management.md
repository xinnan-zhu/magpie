# SSH management

The optional `ssh` built-in plugin manages another computer's complete Magpie
through system OpenSSH. It starts no SSH process until the user connects.
It is a desktop page; the remote service uses the existing authenticated Web
API and its own agent adapters. No remote filesystem is mounted or read by
local agent code.

## Sources of truth and boundaries

- [`plugins/ssh`](../../plugins/ssh/ssh.go) registers the page and disposes
  [`sshremote.Manager`](../../internal/sshremote/manager.go) when switched off.
  [`client.js`](../../internal/gui/assets/plugins/ssh/client.js) uses the
  window kernel and kit; host switches discard late replies. The page follows
  the existing list/settings-row layout. Host actions hold context details;
  tunnel setup and direct-session commands live in their own dialogs.
- [`Host`](../../internal/sshremote/hosts.go) stores names, SSH destinations,
  optional ports and absolute binary paths in `ssh-hosts.json` (0600). It
  stores no SSH credentials and is outside backup/sync. OpenSSH owns host
  trust, keys, ssh-agent, Include, Match and ProxyJump. Unknown/changed host
  keys and interactive authentication must be resolved in a terminal.
- The host editor offers either a concrete alias from the local user's
  `~/.ssh/config` or a manually entered destination and optional port.
  [`ConfigHosts`](../../internal/sshremote/config.go) reads `Host` and `Include`
  only; it never runs SSH, evaluates `Match exec`, or opens `IdentityFile`.
  Aliases keep their original configuration rather than copying resolved
  settings into Magpie; selecting one clears the port override. Includes use
  OpenSSH's `~/.ssh` base, lexical glob order, double quotes, `~/` and
  environment expansion. Conditions are not evaluated, so an alias may only
  apply under its original conditions. Wildcard/negated patterns are omitted.
  Token-dependent or other users' home includes, unreadable files, malformed
  Host/Include lines and resource limits leave the list unavailable, with
  manual entry still offered. No SSH configuration is written. Saving a host
  does not connect to it.
- [`ssh.go`](../../internal/sshremote/ssh.go) runs system `ssh` through `proc`:
  batch mode, strict host checking, no forwarded agent or X11, no shared
  master connection. Management clears configured forwards. Tunnel setup
  refuses aliases with extra configured forwards rather than opening them.
- [`ServeControl`](../../internal/sshremote/control.go) is an early CLI entry
  point, before normal startup and migrations. One bounded JSON request and
  reply travel over stdio. Secrets never go in SSH arguments. `inspect` reads
  process/service context without discovering accounts or agent files.
- [`gui/ssh.go`](../../internal/gui/ssh.go) owns narrow desktop management
  routes. The remote `/api/ssh/context` reports the serving process's real
  user, HOME, configuration, cache, executable, gateway and agent roots.
  Mutations and management reads check this context's target fingerprint on
  their actual HTTP request. A service restarted under another environment
  requires reconnecting, even if the SSH destination stayed the same.

## P0: connect, deploy and open Web

A connection snapshots its host and inspected context; requests use the opaque
connection ID, never a mutable global selected host. A failed probe changes
nothing remotely. Default lookup prefers `~/.local/lib/magpie/ssh/magpie`,
then PATH; an explicit binary path takes precedence. An older binary is only
asked for help, never run with an unknown management command.

Deployment is explicit and supports Linux amd64/arm64. The GUI uses the existing
update feed, mirror and checksum verifier (`update.DownloadCLI`). The CLI also
accepts an offline binary plus its SHA-256. `Deploy` checks the ELF architecture
and digest, uploads via stdin into a private temporary file, checks the digest
again on the target, and atomically replaces the managed binary, retaining
`magpie.previous`. Before replacement, the candidate's side-effect-free help
must advertise SSH control; older published artifacts are rejected. A missing
installation may not replace a managed binary that appeared after its probe.
It preserves PATH installations and refuses a managed
portable `data/` directory or an explicit binary path. A killed upload leaves
the previous executable intact; an uncatchable remote kill can leave
`deploy.lock`, which must be inspected and removed on the target before retry.

For an absent installation, **Install and start Magpie** explicitly chains
the verified upload with [`InstallService`](../../internal/sshremote/deploy.go):
reinspect the uploaded binary, verify the SSH protocol/context, call the
existing `daemon.Install` with authenticated loopback Web on 3430, then poll
read-only until that Web service is ready. It refuses an existing service,
even if one appeared while downloading. Downloads and setup share a
five-minute connection-bound context; startup polling is bounded to 20 seconds.
A lost mutation reply is never retried. The page reinspects after failure so
an uploaded binary with a failed service can resume with a service-only action.
The CLI's explicit offline upload and existing-installation deployment retain
their upload-only behavior; service install/start are also offered separately.
Starting a service preserves its options, executable and environment.
The shell and serving context can differ; agent management always uses the
serving context. This version discovers Magpie's installed user daemon, not
arbitrary system/root services or manually started Web processes. An existing
service without authenticated loopback Web needs `magpie daemon web on` on the
target. Older services need an ordinary update before management works; an
uploaded helper does not replace their service definition.
No sudo or system package manager is run. Linux needs an accessible systemd
user manager. Existing service advice is shown, including the command to enable
user lingering when the service would stop on logout; installation does not
change that operating-system policy by itself.

`OpenWeb` creates an OpenSSH local forward to that service's loopback Web port,
verifies its authenticated context, then opens a one-use local login link. A
local proxy holds the remote Web key; the browser receives a distinct local
session cookie. Host and Origin checks protect the proxy. Reopening Web closes
the previous forward. Closing the connection closes its proxy, forwards and
pending management processes.

## P1: agents and model sources

These modes coexist. None clears existing remote providers or enables LAN.

1. **Manage existing remote setup.** [`ssh_agents.go`](../../internal/gui/ssh_agents.go)
   finds the remote process's original Claude Code and Codex adapters, reads
   their fields and calls `Pick`/`Connect` directly. The normal model menu
   applies immediately. Drift requires review in remote Web. Disconnect uses
   the original `DisconnectPreview` and compares its revision again before
   calling the original `Disconnect`; preview limitations remain those of
   the adapter. User formats, stash and applied records remain remote.
2. **Use the local gateway.** [`StartTunnel`](../../internal/sshremote/tunnel.go)
   allocates a revocable machine key, serves the existing gateway's `SSHFront`
   on a private loopback port and opens `-R 127.0.0.1:<chosen-port>`. The remote
   side checks Linux's actual listening sockets (rejecting wildcard binds),
   then authenticates `/v1/models` before adding a normal Remote Magpie provider.
   `ssh-<host-id>` and `SSHSource` identify that managed upstream. Reconnecting
   rotates only its key and preserves model picks; edited endpoints/headers
   or another source are conflicts to review in Web. Failure does not replay
   inference; retrying a pending creation uses the same upstream identity.
   Closing SSH revokes its key, but leaves the remote provider and picks in
   place, unavailable until reconnect. It adds no fallback.
3. **Copy selected setup.** [`SelectCopy`, `PlanImport`, `ApplyImport`](../../internal/provider/ssh_import.go)
   use a portable subset of the existing providers/groups model. Only API
   providers and explicit routing groups are supported; every dependency must
   be selected. Subscription logins, Remote Magpie providers, Vertex credentials,
   proxies, paths, network settings, fallbacks and advanced request overrides
   are excluded. Primary/secondary API keys and secret headers are opt-in.
   Imports always add new IDs (`-ssh`, then numbered suffixes), remap group
   references and preserve existing entries. The page previews destinations,
   endpoints, models and members without secret values. Apply uses the exact
   in-memory plan, target revision and operation ID; provider writes compare
   revisions under a strict shared file lock. A receipt is committed in the
   same providers.json write. An explicitly retried lost reply returns its
   receipt (last 32 retained); stale previews require another preview.

Default agent wiring uses the remote Magpie gateway. An alternative session
can bypass it: on the remote terminal,
`magpie ssh-direct ssh-<host-id> claude|codex <local-provider/model> [arguments]`.
[`Direct`](../../internal/sshremote/direct.go) starts the original CLI with
per-process endpoint, model and authentication options, without changing agent
files or copying sign-ins. Claude provider switches are cleared for that
process. Codex uses a fresh provider table so an existing user table cannot
supply another token. The local computer and SSH tunnel must remain running.

## Gateway authentication and subscription bridge

[`Server.SSHFront`](../../internal/gateway/ssh.go) shares the current server's
routing and bridge. It authenticates the connection's exact machine key on
all requests, including loopback with LAN off. It allows model discovery and
Claude/Codex inference routes only. Ordinary listeners reject machine keys.
The normal key model allowlists and budgets still apply, and revocation takes
effect on the next request. Remote Magpie forwarding from this ingress is
refused to prevent recursive gateways. No shared-network authorization changes.

An unforgeable Go context scopes session affinity and Claude bridge run/tool
ownership to the connection. Exact tool IDs and remapped continuations both
respect it. Only this transport makes `clientWorkDir` ignore a client-supplied
remote path: ordinary API forwarding and the remote agent's actual tool
working directory are unchanged. Normal local bridge sessions keep their
existing behavior.

## Compatibility, recovery and verification

The first version requires this SSH protocol on both ends, system OpenSSH,
and an authenticated loopback user daemon. Deployment and reverse listener
verification support Linux; an already compatible macOS daemon can be managed
without deployment or the local gateway tunnel. Direct CLI options are tested
with process fixtures; real Claude/Codex versions and real SSH server policies
still need the manual checks in [the validation record](../ssh-validation.md).

The GUI reports authentication/trust/forwarding failures without echoing SSH
configuration or credentials. Operations are bounded and canceled with the
connection. Mutations are never silently retried; an uncertain result asks for
refresh. Provider import is atomic within providers.json; agent mutations retain
the existing adapters' file-level guarantees, not a new cross-file transaction.
Machine keys can be revoked in the existing `magpie gateway-key list|remove <id>` CLI; after a crash,
remove abandoned SSH keys there before creating another connection.

Run the repository checks in [code standards](../code-standards.md), plus
`go test -race -count=20 -tags nogui ./internal/access ./internal/provider ./internal/gateway ./internal/sshremote ./internal/gui -run 'TestSSH|TestForwardedWeb|TestManager'`
under an isolated HOME. `internal/gui/tests/ssh.test.cjs` uses real page assets,
local API fixtures, both browser engines, all five languages and narrow/wide
windows. Transport tests launch a fake SSH executable and local HTTP servers;
none connect to a real host, read a private key, or use a real subscription.
