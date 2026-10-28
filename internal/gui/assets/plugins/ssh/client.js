// SSH owns only its page and connection lifetimes. Provider and agent business
// stays on the selected remote Magpie; a late reply never changes another host.
magpie.plugin("ssh", (ctx) => {
  const { el, button, menu } = ctx.ui;
  let page, hosts = [], selected = "", connection = null, generation = 0, busy = false, agents = [], tunnel = null, tunnelPort = 3431;
  const current = () => hosts.find((h) => h.id === selected);
  const endpoint = (part, c = connection) => "ssh/connections/" + encodeURIComponent(c.id) + "/" + part;
  const heading = (text) => { const h = el("div", "ehead"); h.append(el("b", "", text)); return h; };
  const editing = (ed) => ed.isConnected && !document.getElementById("modal").hidden && !document.getElementById("modal").classList.contains("out");
  function editor() { return el("div", "editor ssh-editor"); }
  async function perform(fn) {
    if (busy) return;
    const g = generation;
    busy = true; draw();
    try { await fn(g); }
    catch (e) { if (g === generation) status(e.message, "err"); }
    finally { if (g === generation) { busy = false; draw(); } }
  }
  function action(label, fn, cls = "text") {
    const b = button(t(label), () => perform(fn), cls);
    b.disabled = busy; return b;
  }
  async function forget() {
    generation++; busy = false; agents = []; tunnel = null;
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
    hosts = out; if (!current()) selected = hosts[0]?.id || ""; draw();
  }
  async function connect(g) {
    const c = await api("ssh/connections", { host: selected });
    if (g !== generation) { await fetch("/api/ssh/connections/" + c.id, { method: "DELETE" }); return; }
    const old = connection; connection = c;
    if (old) await fetch("/api/ssh/connections/" + old.id, { method: "DELETE" });
    if (g !== generation) return; agents = [];
  }
  async function readAgents(g) {
    const c = connection, out = await api(endpoint("agents", c));
    if (g === generation && c === connection) agents = out;
  }
  function addHost(existing = {}) {
    const ed = editor(); ed.classList.add("ssh-host-editor");
    ed.append(heading(t(existing.id ? "Edit" : "Add SSH host")));
    const configLabel = el("label", "", t("SSH configuration")), configSlot = el("div", "ssh-config-picker");
    configLabel.htmlFor = "ssh-host-config"; ed.append(configLabel, configSlot);
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
    const configNote = el("p", "sub"); ed.append(configNote);
    ed.append(el("p", "sub", t("Use a trusted SSH alias or user@host. OpenSSH handles keys, ssh-agent and jump hosts. Authenticate in a terminal first.")));
    const bar = el("div", "bar"), cancel = button(t("Cancel"), closeModal), save = button(t("Save"), async () => {
      save.disabled = cancel.disabled = true;
      try {
        const h = await api("ssh/hosts", { id: existing.id || "", name: inputs.name.value, target: inputs.target.value, port: Number(inputs.port.value) || 0, binary: inputs.binary.value });
        if (!editing(ed)) return; existing = h; await forget(); selected = h.id; await load(); if (editing(ed)) closeModal();
      } catch (e) { if (editing(ed)) status(e.message, "err"); }
      finally { save.disabled = cancel.disabled = false; }
    }, "text primary");
    bar.append(cancel, save); ed.append(bar); openModal(ed); inputs.name.focus({ preventScroll: true });
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
    const ed = editor(), picks = { providers: [], groups: [] };
    ed.append(heading(t("Copy selected configuration")), el("p", "sub", bound.host.name + " · " + bound.inspection.context.config), el("p", "sub", t("Adds copies beside existing entries. Copies endpoints, model picks and basic routing. Sign-ins, proxies, paths, advanced request settings and network settings stay on their own computer.")));
    for (const kind of ["providers", "groups"]) for (const item of choices[kind]) {
      const row = el("label", "ssh-check"), input = el("input"); input.type = "checkbox";
      row.append(input, el("span", "", item.name + " · " + item.id)); ed.append(row); picks[kind].push([input, item.id]);
    }
    const keyRow = el("label", "ssh-check"), keys = el("input"); keys.type = "checkbox";
    keyRow.append(keys, el("span", "", t("Include API keys explicitly"))); ed.append(keyRow);
    const bar = el("div", "bar"), preview = button(t("Preview"), async () => {
      if (g !== generation || bound !== connection) return closeModal();
      preview.disabled = true;
      try {
        const ids = (kind) => picks[kind].filter(([input]) => input.checked).map(([, id]) => id);
        const plan = await api(endpoint("copy/preview", bound), { providers: ids("providers"), groups: ids("groups"), keys: keys.checked });
        if (g !== generation || bound !== connection || !editing(ed)) return;
        ed.replaceChildren(heading(t("Confirm configuration copy")), el("p", "sub", bound.host.name + " · " + bound.inspection.context.config));
        for (const [old, next] of Object.entries(plan.ids)) ed.append(el("code", "ssh-command", old + " → " + next));
        for (const item of plan.entries || []) {
          const detail = el("div", "ssh-copy-entry"); detail.append(el("b", "", item.name + " · " + item.id));
          for (const line of item.details) detail.append(el("code", "ssh-command", line)); ed.append(detail);
        }
        ed.append(el("p", "sub", t(plan.keys ? "API keys will be copied" : "API keys will stay here")));
        const apply = button(t("Apply"), async () => {
          if (g !== generation || bound !== connection) return closeModal();
          apply.disabled = true;
          try { await api(endpoint("copy/apply", bound), { id: plan.id }); if (g !== generation || bound !== connection || !editing(ed)) return; await closeModal(); status(t("Configuration copied")); await readAgents(g); draw(); }
          catch (e) { if (editing(ed)) status(e.message, "err"); }
          finally { apply.disabled = false; }
        }, "text primary");
        const confirm = el("div", "bar"); confirm.append(button(t("Cancel"), closeModal), apply); ed.append(confirm); openModal(ed); apply.focus({ preventScroll: true });
      } catch (e) { status(e.message, "err"); preview.disabled = false; }
    }, "text primary");
    bar.append(button(t("Cancel"), closeModal), preview); ed.append(bar); openModal(ed);
  }
  async function disconnectAgent(g, a) {
    const bound = connection, out = await api(endpoint("preview/" + a.id, bound));
    if (g !== generation || bound !== connection) return;
    const ed = editor(), bar = el("div", "bar");
    ed.append(heading(t("Disconnect agent") + " · " + a.name), changesList(out.changes));
    const apply = button(t("Disconnect"), async () => {
      if (g !== generation || bound !== connection) return closeModal();
      apply.disabled = true;
      try { const result = await api(endpoint("agent", bound), { agent: a.id, action: "disconnect", revision: out.revision }); if (g !== generation || bound !== connection || !editing(ed)) return; agents = result.agents; closeModal(); draw(); }
      catch (e) { if (editing(ed)) status(e.message, "err"); }
      finally { apply.disabled = false; }
    }, "text primary");
    bar.append(button(t("Cancel"), closeModal), apply); ed.append(bar); openModal(ed);
  }
  function section(title, control) {
    const head = el("div", "row-head"); head.append(el("span", "label", t(title)), el("span", "grow"));
    if (control) head.append(control); page.append(head);
    const list = el("div", "list prefs"); page.append(list); return list;
  }
  function setting(list, title, description, control) {
    const val = el("div", "val"); val.append(control);
    list.append(ctx.ui.row(t(title), description, val));
  }
  function environment() {
    const s = connection.inspection, c = s.context, ed = editor();
    ed.append(heading(t("Remote environment")));
    const facts = el("div", "ssh-facts");
    for (const [label, value] of [["Remote environment", [c.hostname, c.user, c.os, c.arch, c.version].filter(Boolean).join(" · ")], ["Configuration directory", c.config], ["Remote Magpie binary", c.binary], ["Remote service", s.service.Running ? t("Running") : t("Stopped")], ["Gateway", c.gateway], ["SSH shell configuration", s.shell && s.shell.target !== c.target ? s.shell.config : ""]]) {
      if (value) facts.append(el("span", "sub", t(label)), el("code", "", value));
    }
    const bar = el("div", "bar"); bar.append(button(t("Close"), closeModal));
    ed.append(facts, bar); openModal(ed);
  }
  function gatewayTunnel() {
    const bound = connection, g = generation, ed = editor();
    ed.append(heading(t("Use local gateway")), el("p", "sub", t("Closing SSH stops its tunnels. Remote services and independent providers keep running.")));
    if (tunnel) {
      ed.append(el("p", "sub", t("Local gateway tunnel is ready. Pick its provider in the remote agents, or run a direct session in the remote terminal.")));
      for (const name of ["claude", "codex"]) {
        const command = "magpie ssh-direct " + tunnel.provider + " " + name + " <provider/model>";
        const line = el("div", "ssh-snippet");
        line.append(el("code", "ssh-command", command), button(t("Copy"), () => copy(command))); ed.append(line);
      }
      const bar = el("div", "bar"); bar.append(button(t("Close"), closeModal)); ed.append(bar);
    } else {
      const label = el("label", "ssh-input"), port = el("input", "words"); port.type = "number"; port.min = "1024"; port.max = "65535"; port.value = tunnelPort;
      label.append(el("span", "", t("Remote tunnel port")), port); ed.append(label);
      const bar = el("div", "bar"), start = button(t("Connect"), async () => {
        start.disabled = true;
        try {
          const out = await api(endpoint("tunnel", bound), { port: Number(port.value) });
          if (g !== generation || bound !== connection) return;
          tunnelPort = Number(port.value); tunnel = out; if (editing(ed)) closeModal(); await readAgents(g); draw();
        } catch (e) { if (editing(ed)) status(e.message, "err"); }
        finally { start.disabled = false; }
      }, "text primary");
      bar.append(button(t("Cancel"), closeModal), start); ed.append(bar);
    }
    openModal(ed);
  }
  function draw() {
    if (!page) return;
    page.replaceChildren();
    if (window.bootPrefs?.web) { page.append(el("p", "sub", t("SSH management is available in the desktop app"))); return; }
    const head = el("div", "row-head");
    head.append(el("span", "label", t("Remote hosts")), el("span", "grow"));
    if (hosts.length > 1) head.append(menu(hosts.map((h) => ({ v: h.id, name: h.name })), selected, t("Remote hosts"), async (id) => { await forget(); selected = id; draw(); }));
    head.append(button(t("Add SSH host"), () => addHost())); page.append(head);
    const card = el("div", "list prefs"); page.append(card);
    if (!hosts.length) { card.append(el("div", "empty-state", t("No SSH hosts yet"))); return; }
    const h = current(), s = connection?.inspection, c = s?.context;
    const row = el("div", "row pref"), who = el("div", "who"), val = el("div", "val");
    who.append(el("div", "name", h.name), el("div", "sub", [h.target, s ? t(s.ready ? "Ready" : "Not ready") : ""].filter(Boolean).join(" · ")));
    if (s?.ready) val.append(action("Open remote Web", async () => api(endpoint("web"), {})));
    else val.append(action("Connect", connect, "text action"));
    const more = button("…", () => {
      openRowMenu(more, [
        ...(connection ? [{ name: "Remote environment", icon: INFO_I, run: environment }, { name: "Reconnect", icon: "", run: () => perform(connect) }, { name: "Disconnect", icon: "", run: () => perform(forget) }] : []),
        { name: "Edit", icon: "", run: () => addHost(h) },
        { name: "Remove", icon: "", run: () => perform(async () => {
          if (!await confirmRemoval(h.name)) return;
          await forget(); const reply = await fetch("/api/ssh/hosts/" + encodeURIComponent(h.id), { method: "DELETE" }); if (!reply.ok) throw new Error(await reply.text()); await load();
        }) },
      ]);
    });
    more.setAttribute("aria-label", t("Host actions")); more.title = t("Host actions");
    val.append(more); row.append(who, val); card.append(row);
    if (!connection) { for (const b of page.querySelectorAll("button")) b.disabled = busy; return; }
    if (s.problem) card.append(el("p", "ssh-note sub", s.problem));
    if (!s.ready) {
      const verb = s.service.Installed ? "start" : "install";
      const control = !s.installed || !s.compatible
        ? action("Deploy verified Magpie", async (g) => { await api(endpoint("deploy"), {}); await connect(g); })
        : action(verb === "start" ? "Start remote service" : "Install and start remote service", async (g) => { await api(endpoint("service"), { action: verb }); await connect(g); });
      setting(card, "SSH management", c.config || h.target, control);
    } else {
      const sources = section("Model sources");
      setting(sources, "Use local gateway", tunnel ? t("Ready") + " · " + tunnel.provider : t("Use this computer’s models while SSH is connected"), action(tunnel ? "Details" : "Connect", async () => gatewayTunnel()));
      setting(sources, "Copy selected configuration", t("Add providers and routing groups that run independently on the remote host"), action("Select", copyConfig));
      const list = section("Agents", action("Manage remote agents", readAgents));
      if (!agents.length) list.append(el("div", "ssh-note sub", t("Manage Claude Code and Codex in the remote environment")));
      for (const a of agents) {
        const ar = el("div", "row pref"), aw = el("div", "who"), av = el("div", "val");
        if (a.icon) ar.append(icon(a.icon));
        aw.append(el("div", "name", a.name), el("div", "sub", a.drift ? t("Configuration changed; review it in the remote Web page") : a.path)); ar.append(aw);
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
        av.append(action(a.wired ? "Disconnect agent" : "Connect agent", async (g) => {
          if (a.wired) return disconnectAgent(g, a);
          const bound = connection, out = await api(endpoint("agent", bound), { agent: a.id, action: "connect" });
          if (g === generation && bound === connection) agents = out.agents;
        }));
        ar.append(av); list.append(ar);
      }
    }
    for (const b of page.querySelectorAll("button")) b.disabled = busy;
  }
  ctx.onDispose(() => { if (document.querySelector(".ssh-editor")) closeModal(); forget().catch(() => {}); });
  ctx.slots.add("page", { key: "ssh", title: "Remote hosts", mount(main) { page = main; }, open: () => window.bootPrefs?.web ? draw() : load(), relang: draw });
});
