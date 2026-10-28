// SSH owns only its page and connection lifetimes. Provider and agent business
// stays on the selected remote Magpie; a late reply never changes another host.
// The page is laid out as Providers and Agents are: every saved host is a row
// of one list with ＋ Add under it, its state a dot and a line under its name,
// its actions in the row's menu; the connected host's model sources and agents
// follow in settings rows.
magpie.plugin("ssh", (ctx) => {
  const { el, button, menu, toggle } = ctx.ui;
  const HOST = "M2.5 3h11a1 1 0 0 1 1 1v6.5a1 1 0 0 1-1 1h-11a1 1 0 0 1-1-1V4a1 1 0 0 1 1-1zM5.5 14h5M8 11.5V14M4.5 5.5 6 7 4.5 8.5M8 8.5h2.5";
  const TRASH = "M3 4.5h10M6.5 4.5V3h3v1.5M4.5 4.5l.6 8.5h5.8l.6-8.5M7 7v4M9 7v4";
  // agents is null until the connected host's are read
  let page, hosts = [], selected = "", connecting = "", connection = null, generation = 0, busy = false, installing = false, agents = null, tunnel = null, tunnelPort = 3431;
  const endpoint = (part, c = connection) => "ssh/connections/" + encodeURIComponent(c.id) + "/" + part;
  const editing = (ed) => ed.isConnected && !document.getElementById("modal").hidden && !document.getElementById("modal").classList.contains("out");
  function editor(title, cls = "") {
    const ed = el("div", "editor ssh-editor " + cls), head = el("div", "ehead"), glyph = el("span", "ic ssh-glyph");
    glyph.append(svg(HOST, 16, 1.4)); head.append(glyph, el("b", "", title)); ed.append(head);
    return ed;
  }
  // the dialog's foot: what it does at the right, as every editor has it
  function foot(ed, ...buttons) {
    const bar = el("div", "bar"); bar.append(el("span", "grow"), ...buttons); ed.append(bar);
  }
  // a dot and the words beside it, as an agent's line under its name says its state
  function stateLine(words, kind = "") {
    const line = el("div", "ag-st ssh-st" + (kind ? " " + kind : ""));
    line.append(el("span", "ag-dot"), el("span", "ag-st-t", words));
    return line;
  }
  async function perform(fn) {
    if (busy) return;
    const g = generation;
    busy = true; draw();
    try { await fn(g); }
    catch (e) { if (g === generation) status(e.message, "err"); }
    finally { if (g === generation) { busy = false; draw(); } }
  }
  function action(label, fn, cls = "text action") {
    const b = button(t(label), () => perform(fn), cls);
    b.disabled = busy; return b;
  }
  async function forget() {
    generation++; busy = false; installing = false; connecting = ""; agents = null; tunnel = null;
    const old = connection; connection = null;
    if (old) await fetch("/api/ssh/connections/" + encodeURIComponent(old.id), { method: "DELETE" });
  }
  async function load() {
    const g = generation, out = await api("ssh/hosts"); if (g !== generation) return;
    if (connection) {
      const next = out.find((h) => h.id === connection.host.id);
      if (!next || ["target", "port", "binary"].some((key) => (next[key] || "") !== (connection.host[key] || ""))) {
        await forget(); if (generation !== g + 1) return;
      }
    }
    hosts = out; if (!hosts.some((h) => h.id === selected)) selected = hosts[0]?.id || ""; draw();
  }
  async function readAgents(g) {
    const c = connection, out = await api(endpoint("agents", c));
    if (g === generation && c === connection) agents = out;
  }
  // a ready host's agents are read with it, as the Agents page lists them
  async function connect(g) {
    const c = await api("ssh/connections", { host: selected });
    if (g !== generation) { await fetch("/api/ssh/connections/" + c.id, { method: "DELETE" }); return; }
    const old = connection; connection = c;
    if (old) await fetch("/api/ssh/connections/" + old.id, { method: "DELETE" });
    if (g !== generation) return; agents = null;
    if (c.inspection.ready) await readAgents(g).catch((e) => { if (g === generation) status(e.message, "err"); });
  }
  // one host is connected at a time: another's Connect lets the first go
  async function open(h) {
    if (busy) return;
    if (connection) await forget();
    selected = connecting = h.id;
    await perform(async (g) => { try { await connect(g); } finally { if (g === generation) connecting = ""; } });
  }
  async function deploy(g, start) {
    const bound = connection;
    installing = start; draw();
    try { await api(endpoint("deploy", bound), { start }); }
    catch (e) {
      if (g === generation && bound === connection) await connect(g).catch(() => {});
      throw e;
    }
    finally { if (g === generation) installing = false; }
    if (g === generation && bound === connection) await connect(g);
  }
  function addHost(existing = {}) {
    const ed = editor(t(existing.id ? "Edit" : "Add SSH host"), "ssh-host-editor");
    const configLabel = el("label", "", t("SSH configuration")), configSlot = el("div", "ssh-config-picker");
    configLabel.htmlFor = "ssh-host-config"; ed.append(configLabel, configSlot);
    const configNote = el("div", "hint ssh-config-note"); configSlot.after(configNote);
    let aliases = [], selectedAlias = "";
    function configFields() {
      inputs.target.readOnly = !!selectedAlias; inputs.port.disabled = !!selectedAlias;
      for (const key of ["target", "port"]) inputs[key].hidden = inputs[key].labels[0].hidden = !!selectedAlias;
    }
    function configPicker(loading = false) {
      const pick = menu([{ v: "", name: t("Enter manually") }, ...aliases.map((v) => ({ v, name: v }))], selectedAlias, t("SSH configuration"), (alias) => {
        if (alias) {
          if (!inputs.name.value || inputs.name.value === selectedAlias) inputs.name.value = alias;
          inputs.target.value = alias; inputs.port.value = "";
        }
        selectedAlias = alias;
        configFields();
      });
      pick.id = "ssh-host-config"; pick.setAttribute("aria-label", t("SSH configuration")); pick.disabled = loading || !aliases.length;
      configSlot.replaceChildren(pick);
    }
    const inputs = {};
    for (const [key, label, hint] of [["name", "Name", ""], ["target", "SSH destination", "user@host"], ["port", "SSH port", "22"], ["binary", "Remote Magpie binary", "/home/user/.local/bin/magpie"]]) {
      const labelEl = el("label", "", t(label)), input = el("input", "words");
      input.id = "ssh-host-" + key; labelEl.htmlFor = input.id;
      input.type = "text"; input.value = existing[key] || ""; input.autocomplete = "off"; input.spellcheck = false; input.placeholder = hint;
      if (key === "port") { input.type = "number"; input.min = "1"; input.max = "65535"; }
      ed.append(labelEl, input); inputs[key] = input;
    }
    ed.append(el("div", "hint", t("Leave empty to use the managed install, then the remote PATH")));
    ed.append(el("p", "ssh-say", t("Use a trusted SSH alias or user@host. OpenSSH handles keys, ssh-agent and jump hosts. Authenticate in a terminal first.")));
    const cancel = button(t("Cancel"), closeModal), save = button(t("Save"), async () => {
      save.disabled = cancel.disabled = true;
      try {
        const h = await api("ssh/hosts", { id: existing.id || "", name: inputs.name.value, target: inputs.target.value, port: Number(inputs.port.value) || 0, binary: inputs.binary.value });
        if (!editing(ed)) return; existing = h; if (connection?.host.id === h.id) await forget(); selected = h.id; await load(); if (editing(ed)) closeModal();
      } catch (e) { if (editing(ed)) status(e.message, "err"); }
      finally { save.disabled = cancel.disabled = false; }
    }, "text primary");
    foot(ed, cancel, save); openModal(ed); inputs.name.focus({ preventScroll: true });
    configPicker(true);
    api("ssh/config-hosts").then((out) => {
      if (!editing(ed)) return;
      aliases = out;
      if (!Number(inputs.port.value) && inputs.target.value === existing.target && aliases.includes(existing.target)) {
        selectedAlias = existing.target; configFields();
      }
      configPicker();
      if (!aliases.length) configNote.textContent = t("No hosts found in ~/.ssh/config. Enter a destination manually.");
    }).catch(() => {
      if (!editing(ed)) return;
      configPicker(); configNote.textContent = t("Could not read SSH configuration. Enter a destination manually.");
    });
  }
  async function copyConfig(g) {
    const bound = connection, choices = await api("ssh/copy-choices");
    if (g !== generation || bound !== connection) return;
    const ed = editor(t("Copy selected configuration"), "ssh-copy"), picks = { providers: [], groups: [] };
    ed.append(el("p", "ssh-say", t("Adds copies beside existing entries. Copies endpoints, model picks and basic routing. Sign-ins, proxies, paths, advanced request settings and network settings stay on their own computer.")));
    // each kind its own list of rows, as a settings group
    for (const [kind, title] of [["providers", "Providers"], ["groups", "Routing groups"]]) {
      if (!choices[kind].length) continue;
      const list = el("div", "list ssh-picks");
      for (const item of choices[kind]) {
        const row = el("label", "row ssh-check"), input = el("input"); input.type = "checkbox";
        const who = el("span", "who"); who.append(el("span", "name", item.name), el("span", "sub", item.id));
        row.append(input, who); list.append(row); picks[kind].push([input, item.id]);
      }
      ed.append(el("div", "ssh-picks-head", t(title)), list);
    }
    const keyList = el("div", "list ssh-picks"), keyRow = el("label", "row ssh-check"), keys = el("input"); keys.type = "checkbox";
    keyRow.append(keys, el("span", "name", t("Include API keys explicitly"))); keyList.append(keyRow); ed.append(keyList);
    const preview = button(t("Preview"), async () => {
      if (g !== generation || bound !== connection) return closeModal();
      preview.disabled = true;
      try {
        const ids = (kind) => picks[kind].filter(([input]) => input.checked).map(([, id]) => id);
        const plan = await api(endpoint("copy/preview", bound), { providers: ids("providers"), groups: ids("groups"), keys: keys.checked });
        if (g !== generation || bound !== connection || !editing(ed)) return;
        const sure = editor(t("Confirm configuration copy"), "ssh-copy");
        sure.append(el("p", "ssh-say", bound.host.name + " · " + bound.inspection.context.config));
        const changes = el("div", "ssh-changes");
        for (const [old, next] of Object.entries(plan.ids)) changes.append(el("code", "ssh-command", old + " → " + next));
        for (const item of plan.entries || []) {
          const detail = el("div", "ssh-copy-entry"); detail.append(el("b", "", item.name + " · " + item.id));
          for (const line of item.details) detail.append(el("code", "ssh-command", line)); changes.append(detail);
        }
        sure.append(changes, stateLine(t(plan.keys ? "API keys will be copied" : "API keys will stay here"), plan.keys ? "wait" : ""));
        const apply = button(t("Apply"), async () => {
          if (g !== generation || bound !== connection) return closeModal();
          apply.disabled = true;
          try { await api(endpoint("copy/apply", bound), { id: plan.id }); if (g !== generation || bound !== connection || !editing(sure)) return; await closeModal(); status(t("Configuration copied")); await readAgents(g); draw(); }
          catch (e) { if (editing(sure)) status(e.message, "err"); }
          finally { apply.disabled = false; }
        }, "text primary");
        foot(sure, button(t("Cancel"), closeModal), apply); openModal(sure); apply.focus({ preventScroll: true });
      } catch (e) { status(e.message, "err"); preview.disabled = false; }
    }, "text primary");
    foot(ed, button(t("Cancel"), closeModal), preview); openModal(ed);
  }
  async function disconnectAgent(g, a) {
    const bound = connection, out = await api(endpoint("preview/" + a.id, bound));
    if (g !== generation || bound !== connection) return;
    const ed = editor(t("Disconnect agent") + " · " + a.name);
    ed.append(changesList(out.changes));
    const apply = button(t("Disconnect"), async () => {
      if (g !== generation || bound !== connection) return closeModal();
      apply.disabled = true;
      try { const result = await api(endpoint("agent", bound), { agent: a.id, action: "disconnect", revision: out.revision }); if (g !== generation || bound !== connection || !editing(ed)) return; agents = result.agents; closeModal(); draw(); }
      catch (e) { if (editing(ed)) status(e.message, "err"); }
      finally { apply.disabled = false; }
    }, "text primary");
    foot(ed, button(t("Cancel"), closeModal), apply); openModal(ed);
  }
  function environment() {
    const s = connection.inspection, c = s.context, ed = editor(t("Remote environment"));
    const facts = el("div", "list ssh-facts");
    for (const [label, value] of [["Remote environment", [c.hostname, c.user, c.os, c.arch, c.version].filter(Boolean).join(" · ")], ["Configuration directory", c.config], ["Remote Magpie binary", c.binary], ["Remote service", s.service.Running ? t("Running") : t("Stopped")], ["Gateway", c.gateway], ["SSH shell configuration", s.shell && s.shell.target !== c.target ? s.shell.config : ""]]) {
      if (!value) continue;
      const row = el("div", "row"); row.append(el("span", "ssh-fact", t(label)), el("code", "", value)); facts.append(row);
    }
    ed.append(facts); foot(ed, button(t("Close"), closeModal, "text primary")); openModal(ed);
  }
  function gatewayTunnel() {
    const bound = connection, g = generation, ed = editor(t("Use local gateway"));
    ed.append(el("p", "ssh-say", t("Closing SSH stops its tunnels. Remote services and independent providers keep running.")));
    if (tunnel) {
      ed.append(el("p", "ssh-say", t("Local gateway tunnel is ready. Pick its provider in the remote agents, or run a direct session in the remote terminal.")));
      const snippets = el("div", "ssh-snippets");
      for (const name of ["claude", "codex"]) {
        const command = "magpie ssh-direct " + tunnel.provider + " " + name + " <provider/model>";
        const line = el("div", "ssh-snippet"), copier = el("button", "copy");
        copier.type = "button"; copier.title = t("Copy"); copier.setAttribute("aria-label", t("Copy")); copier.append(svg(COPY_ICON, 13, 1.5));
        copier.onclick = () => copy(command);
        line.append(el("code", "ssh-command", command), copier); snippets.append(line);
      }
      ed.append(snippets); foot(ed, button(t("Close"), closeModal, "text primary"));
    } else {
      const label = el("label", "", t("Remote tunnel port")), port = el("input", "words"); port.type = "number"; port.min = "1024"; port.max = "65535"; port.value = tunnelPort;
      port.id = "ssh-tunnel-port"; label.htmlFor = port.id;
      const field = el("div", "ssh-port"); field.append(label, port); ed.append(field);
      const start = button(t("Connect"), async () => {
        start.disabled = true;
        try {
          const out = await api(endpoint("tunnel", bound), { port: Number(port.value) });
          if (g !== generation || bound !== connection) return;
          tunnelPort = Number(port.value); tunnel = out; if (editing(ed)) closeModal(); await readAgents(g); draw();
        } catch (e) { if (editing(ed)) status(e.message, "err"); }
        finally { start.disabled = false; }
      }, "text primary");
      foot(ed, button(t("Cancel"), closeModal), start);
    }
    openModal(ed);
  }
  // what a connected host still needs before it is ready, with the click that does it
  function needs(s) {
    if (!s.installed) return [t("Magpie is not installed"), action(installing ? "Installing Magpie…" : "Install and start Magpie", (g) => deploy(g, true), "text primary")];
    if (!s.compatible) return [t("This Magpie is too old to manage over SSH"), action("Deploy verified Magpie", (g) => deploy(g, false), "text primary")];
    // a service that runs and still isn't ready says why under the row; trying again is all the page can do
    if (s.service.Installed && s.service.Running) return [t("Remote Magpie is not ready"), action("Reconnect", connect)];
    const verb = s.service.Installed ? "start" : "install";
    return [t("Remote service is not running"), action(verb === "start" ? "Start remote service" : "Install and start remote service", async (g) => { await api(endpoint("service"), { action: verb }); await connect(g); }, "text primary")];
  }
  function hostRow(list, h) {
    const on = connection?.host.id === h.id, s = on ? connection.inspection : null;
    const row = el("div", "row ssh-host" + (on ? " on" : "")), glyph = el("span", "ic ssh-glyph"), who = el("div", "who"), val = el("div", "val");
    row.dataset.id = h.id; glyph.append(svg(HOST, 16, 1.4));
    const where = h.port ? h.target + " · " + t("SSH port") + " " + h.port : h.target;
    let line;
    if (connecting === h.id) line = stateLine(t("Connecting…"), "wait");
    else if (s?.ready) line = stateLine(t("Connected · {where}", { where }), "on");
    else if (s) { const [why, fix] = needs(s); line = stateLine(why + " · " + where, "wait"); val.append(fix); }
    else line = stateLine(where);
    who.append(el("div", "name", h.name), line);
    if (s?.ready) val.append(action("Open remote Web", async () => api(endpoint("web"), {})));
    else if (!s) val.append(button(t("Connect"), () => open(h), "text action"));
    const more = el("button", "icon ssh-more");
    more.type = "button"; more.title = t("Host actions"); more.setAttribute("aria-label", t("Host actions")); more.setAttribute("aria-haspopup", "menu");
    more.append(svg(DOTS, 14, 2.2));
    const acts = () => [
      ...(on ? [
        { name: "Remote environment", icon: INFO_I, run: environment },
        { name: "Reconnect", icon: REAPPLY, run: () => perform(connect) },
        { name: "Disconnect", icon: UNPLUG, run: async () => { await forget(); draw(); } },
      ] : []),
      { name: "Edit", icon: PENCIL, sep: on, run: () => addHost(h) },
      { name: "Remove", icon: TRASH, run: () => perform(async () => {
        if (!await confirmRemoval(h.name)) return;
        if (on) await forget();
        const reply = await fetch("/api/ssh/hosts/" + encodeURIComponent(h.id), { method: "DELETE" }); if (!reply.ok) throw new Error(await reply.text()); await load();
      }) },
    ];
    const menuFor = () => { if (!busy) openRowMenu(more, acts()); };
    more.onclick = (e) => { e.stopPropagation(); if (more.classList.contains("open")) return closeAgentMenu(); menuFor(); };
    row.oncontextmenu = (e) => { e.preventDefault(); menuFor(); };
    val.append(more); row.append(glyph, who, val); list.append(row);
    if (!s) return;
    if (s.problem) list.append(el("p", "ssh-note", s.problem));
    for (const advice of s.service.Advice || []) list.append(el("p", "ssh-note", advice));
  }
  function section(title, control) {
    const head = el("div", "row-head"); head.append(el("span", "label", t(title)), el("span", "grow"));
    if (control) head.append(control); page.append(head);
    const list = el("div", "list prefs"); page.append(list); return list;
  }
  function setting(list, title, description, ...controls) {
    const val = el("div", "val"); val.append(...controls);
    list.append(ctx.ui.row(t(title), description, val));
  }
  function agentRow(list, a) {
    const ar = el("div", "row pref ssh-agent"), aw = el("div", "who"), av = el("div", "val");
    ar.dataset.id = a.id;
    aw.append(el("div", "name", a.name), a.drift ? stateLine(t("Configuration changed; review it in the remote Web page"), "wait") : stateLine(a.path, a.wired ? "on" : ""));
    for (const f of a.fields.filter((f) => f.key === "model")) {
      av.append(menu(f.options.map((o) => ({ v: o.value, name: o.label || o.value })), f.value, a.name, async (value) => {
        if (busy) return;
        const g = generation, bound = connection;
        busy = true; draw();
        try { const out = await api(endpoint("pick", bound), { agent: a.id, field: f.key, value }); if (g === generation) { agents = out.agents; if (out.notice) status(out.notice); } }
        catch (e) { if (g === generation) status(e.message, "err"); }
        finally { if (g === generation) { busy = false; draw(); } }
      }));
    }
    // the switch the Agents page has: on connects at once, off shows what goes first
    av.append(toggle(!!a.wired, a.name, () => perform(async (g) => {
      if (a.wired) return disconnectAgent(g, a);
      const bound = connection, out = await api(endpoint("agent", bound), { agent: a.id, action: "connect" });
      if (g === generation && bound === connection) agents = out.agents;
    })));
    ar.append(icon(a.icon || "generic"), aw, av); list.append(ar);
  }
  function draw() {
    if (!page) return;
    page.replaceChildren();
    if (window.bootPrefs?.web) { page.append(el("p", "set-none", t("SSH management is available in the desktop app"))); return; }
    const list = el("div", "list ssh-hosts"); page.append(list);
    if (!hosts.length) {
      const empty = el("div", "empty-state"); empty.append(el("b", "", t("No SSH hosts yet")), el("span", "", t("Manage another computer's Magpie through system SSH")));
      list.append(empty);
    }
    for (const h of hosts) hostRow(list, h);
    const after = el("div", "after-list"); after.append(button(t("＋ Add SSH host"), () => addHost())); page.append(after);
    if (connection?.inspection.ready) {
      const sources = section("Model sources");
      setting(sources, "Use local gateway", tunnel ? "" : t("Use this computer’s models while SSH is connected"),
        ...(tunnel ? [stateLine(tunnel.provider, "on"), action("Details", async () => gatewayTunnel())] : [action("Connect", async () => gatewayTunnel())]));
      setting(sources, "Copy selected configuration", t("Add providers and routing groups that run independently on the remote host"), action("Select", copyConfig));
      const refresh = el("button", "icon ssh-refresh");
      refresh.type = "button"; refresh.title = t("Refresh"); refresh.setAttribute("aria-label", t("Refresh")); refresh.append(svg(REAPPLY, 13, 1.5));
      refresh.onclick = () => perform(readAgents);
      const list = section("Agents", refresh);
      if (agents === null) list.append(el("div", "ssh-empty", t(busy ? "Reading remote agents…" : "Manage Claude Code and Codex in the remote environment")));
      else if (!agents.length) list.append(el("div", "ssh-empty", t("Manage Claude Code and Codex in the remote environment")));
      else for (const a of agents) agentRow(list, a);
    }
    for (const b of page.querySelectorAll("button")) b.disabled = busy;
  }
  ctx.onDispose(() => { if (document.querySelector(".ssh-editor")) closeModal(); forget().catch(() => {}); });
  ctx.slots.add("page", { key: "ssh", title: "Remote hosts", mount(main) { page = main; }, open: () => window.bootPrefs?.web ? draw() : load(), relang: draw });
});
