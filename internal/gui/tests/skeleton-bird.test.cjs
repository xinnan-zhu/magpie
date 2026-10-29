// Run with Node's test runner and Playwright on the module path; see README.md.
// What is loading has magpie's bird on it (the owner, 10-26): skeletons
// still up after a moment, as on a slow page, get the bird, which hops from
// bar to bar, each bar dipping under it, and flies off once they are gone.
// A quick load never sees it, and with reduced motion no bird comes.
// Settings' Loading bird (Yayoi_no_yume on X, a 1-core server) turns it
// off: no bird comes and nothing looks for skeletons. With it on, the look
// every 250ms runs only while a skeleton is in the page.
const assert = require("node:assert/strict");
const fs = require("node:fs/promises");
const path = require("node:path");
const { test } = require("node:test");
const { chromium, webkit } = require("playwright");

const assets = path.resolve(__dirname, "../assets");
const listings = [{ package: "opencode-copilot-auth", name: "GitHub Copilot", icon: "githubcopilot", providers: ["github-copilot"],
  summary: { en: "Your GitHub Copilot plan." }, npm: { version: "0.0.9", weekly: 48213, publisher: "thdxr" } }];

function server(market, prefs = {}, posted = []) {
  return async (route) => {
    const url = new URL(route.request().url());
    const json = (data) => route.fulfill({ json: data });
    if (url.pathname === "/boot.js") return route.fulfill({ contentType: "text/javascript", body: `window.bootPrefs = ${JSON.stringify({ lang: "en", theme: "light", web: false, ...(prefs.noBird ? { noBird: true } : {}) })};` });
    if (url.pathname === "/wails/runtime.js") return route.fulfill({ contentType: "text/javascript", body: "export const Window = {};" });
    const settings = () => ({ lang: "en", theme: "light", searchVendors: [], searchAPIs: [], ...prefs });
    if (url.pathname === "/api/state") return json({ agents: [], profiles: [], settings: settings() });
    if (url.pathname === "/api/settings") {
      if (route.request().method() === "POST") { const b = JSON.parse(route.request().postData()); posted.push(b); Object.assign(prefs, b); }
      return json(settings());
    }
    if (url.pathname === "/api/providers") return json({ providers: [], presets: [], excluded: [], gateway: { running: true, window: true }, plugins: [] });
    if (url.pathname === "/api/groups") return json({ groups: [], pools: [] });
    const state = { bun: true, bunVersion: "1.3.0", plugins: [] };
    if (url.pathname === "/api/plugins/market") { await market(); return json({ listings, state }); }
    if (url.pathname === "/api/plugins") return json(state);
    if (url.pathname === "/api/plugins/listings") { await market(); return json({ listings }); }
    if (url.pathname.startsWith("/api/")) return json({});
    if (!/^\/[\w./-]*$/.test(url.pathname) || url.host !== "magpie.test") return route.fulfill({ status: 404, body: "" });
    const file = path.join(assets, url.pathname === "/" ? "index.html" : url.pathname);
    const contentType = { ".html": "text/html", ".js": "text/javascript", ".css": "text/css", ".svg": "image/svg+xml", ".png": "image/png" }[path.extname(file)];
    try { await route.fulfill({ body: await fs.readFile(file), contentType }); } catch { await route.fulfill({ status: 404, body: "" }); }
  };
}

async function open(browser, reduced, market, prefs, posted, view = "plugins") {
  const page = await (await browser.newContext({ viewport: { width: 980, height: 820 }, reducedMotion: reduced ? "reduce" : "no-preference" })).newPage();
  page.setDefaultTimeout(5000);
  const errors = [];
  page.on("pageerror", (e) => errors.push(e.message));
  await page.addInitScript(() => {
    // every bird that came, and every bar that dipped
    window.seen = { birds: 0, places: new Set(), dips: 0 };
    // the skeletons' look every 250ms: how many were started, how many run
    window.looks = { made: 0, live: new Set() };
    const si = window.setInterval, ci = window.clearInterval;
    window.setInterval = function (fn, ms, ...a) {
      const id = si.call(window, fn, ms, ...a);
      if (typeof fn === "function" && /skelSeen/.test(String(fn))) looks.made++, looks.live.add(id);
      return id;
    };
    window.clearInterval = function (id) { looks.live.delete(id); return ci.call(window, id); };
    new MutationObserver((ms) => { for (const m of ms) for (const n of m.addedNodes) if (n.classList?.contains("sk-bird")) seen.birds++; })
      .observe(document, { childList: true, subtree: true });
    const step = () => {
      const b = document.querySelector(".sk-bird.roam");
      if (b) seen.places.add(b.style.transform);
      seen.dips = Math.max(seen.dips, document.getAnimations().filter((a) => a.effect?.target?.matches?.(".skeleton, .pm-card.ghost .sk")
        && a.effect.getKeyframes().some((k) => /translateY/.test(k.transform || ""))).length);
      requestAnimationFrame(step);
    };
    requestAnimationFrame(step);
  });
  await page.route("**/*", server(market, prefs, posted));
  await page.goto("http://magpie.test/?view=" + view);
  return { page, errors };
}

for (const engine of (process.env.BROWSER ? [process.env.BROWSER] : ["chromium", "webkit"])) {
  const launch = () => (engine === "webkit" ? webkit.launch() : chromium.launch({ channel: "chromium" }));

  test(engine + ": a slow page's skeletons get the bird, hopping bar to bar, and it flies off when they go", async (t) => {
    const browser = await launch();
    t.after(() => browser.close());
    let release;
    const held = new Promise((r) => { release = r; });
    const { page, errors } = await open(browser, false, () => held);
    await page.locator("#view-plugins .pm-card.ghost").first().waitFor();
    await page.locator(".sk-bird.roam").waitFor({ state: "attached", timeout: 3000 });
    await page.waitForFunction(() => seen.places.size > 30 && seen.dips > 0, null, { timeout: 4000 });
    const feet = await page.evaluate(() => {
      const r = document.querySelector(".sk-bird.roam svg").getBoundingClientRect();
      return { w: r.width, h: r.height };
    });
    assert(feet.w > 10 && feet.h > 10, `the bird is drawn: ${JSON.stringify(feet)}`);
    release();
    await page.waitForFunction(() => !document.querySelector("#view-plugins .pm-card.ghost"));
    await page.waitForFunction(() => !document.querySelector(".sk-bird"), null, { timeout: 2000 });
    assert.deepEqual(errors, []);
  });

  test(engine + ": a quick load never sees the bird", async (t) => {
    const browser = await launch();
    t.after(() => browser.close());
    const { page, errors } = await open(browser, false, () => new Promise((r) => setTimeout(r, 150)));
    await page.waitForFunction(() => !document.querySelector("#view-plugins .pm-card.ghost"));
    await page.waitForTimeout(800);
    assert.equal(await page.evaluate(() => seen.birds), 0, "no bird came");
    assert.deepEqual(errors, []);
  });

  test(engine + ": with reduced motion no bird comes and no bar moves", async (t) => {
    const browser = await launch();
    t.after(() => browser.close());
    let release;
    const held = new Promise((r) => { release = r; });
    const { page, errors } = await open(browser, true, () => held);
    await page.locator("#view-plugins .pm-card.ghost").first().waitFor();
    await page.waitForTimeout(1200);
    assert.deepEqual(await page.evaluate(() => [seen.birds, seen.dips]), [0, 0]);
    release();
    await page.waitForFunction(() => !document.querySelector("#view-plugins .pm-card.ghost"));
    assert.deepEqual(errors, []);
  });

  test(engine + ": with no skeleton left, nothing looks for one every 250ms", async (t) => {
    const browser = await launch();
    t.after(() => browser.close());
    let release;
    const held = new Promise((r) => { release = r; });
    const { page, errors } = await open(browser, false, () => held);
    await page.locator("#view-plugins .pm-card.ghost").first().waitFor();
    await page.waitForFunction(() => looks.live.size === 1);
    release();
    await page.waitForFunction(() => !document.querySelector("#view-plugins .pm-card.ghost"));
    await page.waitForFunction(() => !document.querySelector(".sk-bird") && looks.live.size === 0, null, { timeout: 3000 });
    // a skeleton added later starts it again
    const made = await page.evaluate(() => looks.made);
    await page.evaluate(() => { const s = document.createElement("span"); s.className = "skeleton"; s.id = "late-sk"; document.body.append(s); });
    await page.waitForFunction((n) => looks.made === n + 1 && looks.live.size === 1, made);
    await page.evaluate(() => document.getElementById("late-sk").remove());
    await page.waitForFunction(() => looks.live.size === 0);
    assert.deepEqual(errors, []);
  });

  test(engine + ": with Settings' Loading bird off, no bird comes, no bar moves and nothing looks for skeletons", async (t) => {
    const browser = await launch();
    t.after(() => browser.close());
    let release;
    const held = new Promise((r) => { release = r; });
    const { page, errors } = await open(browser, false, () => held, { noBird: true });
    await page.locator("#view-plugins .pm-card.ghost").first().waitFor();
    await page.waitForTimeout(1500);
    assert.deepEqual(await page.evaluate(() => [seen.birds, seen.dips, looks.made, looks.live.size]), [0, 0, 0, 0]);
    release();
    await page.waitForFunction(() => !document.querySelector("#view-plugins .pm-card.ghost"));
    assert.deepEqual(errors, []);
  });

  test(engine + ": Settings' Loading bird is On by default, and Off saves noBird and stops the look", async (t) => {
    const browser = await launch();
    t.after(() => browser.close());
    const prefs = {}, posted = [];
    const { page, errors } = await open(browser, false, async () => {}, prefs, posted, "settings");
    const on = page.locator("#birdSegs .opt", { hasText: /^On$/ }), off = page.locator("#birdSegs .opt", { hasText: /^Off$/ });
    await on.waitFor();
    assert.match(await on.getAttribute("class"), /\bon\b/);
    // a skeleton up while it is turned off: the look stops, and one added
    // after never starts it
    await page.evaluate(() => { const s = document.createElement("span"); s.className = "skeleton"; document.body.append(s); });
    await page.waitForFunction(() => looks.live.size === 1);
    await off.click();
    await page.waitForFunction(() => document.documentElement.dataset.bird === "off" && looks.live.size === 0);
    assert.equal(posted.at(-1).noBird, true);
    const made = await page.evaluate(() => looks.made);
    await page.evaluate(() => { const s = document.createElement("span"); s.className = "skeleton"; document.body.append(s); });
    await page.waitForTimeout(600);
    assert.equal(await page.evaluate(() => looks.made), made, "nothing starts looking with it off");
    await on.click();
    await page.waitForFunction(() => !document.documentElement.dataset.bird && looks.live.size === 1);
    assert.equal(posted.at(-1).noBird, false);
    assert.deepEqual(errors, []);
  });
}
