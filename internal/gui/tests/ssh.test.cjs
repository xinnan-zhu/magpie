// System SSH is never run here: the real page talks only to these fixtures.
const assert = require('node:assert/strict');
const fs = require('node:fs/promises');
const path = require('node:path');
const { test } = require('node:test');
const { chromium, webkit } = require('playwright');
const assets = path.resolve(__dirname, '../assets');
const { I18N } = require('./strings.cjs').strings();

for (const engine of (process.env.BROWSER ? [process.env.BROWSER] : ['chromium', 'webkit'])) {
  test(`${engine}: SSH host context, immediate picks, explicit copy and disposal`, async (t) => {
    const browser = await (engine === 'webkit' ? webkit.launch() : chromium.launch({channel:'chromium'}));
    t.after(() => browser.close());
    for (const lang of ['en','zh','zh-TW','ja','de']) for (const width of [1100,420]) {
      const words = (s) => I18N[lang]?.[s] || s;
      const context = await browser.newContext({viewport:{width,height:820},reducedMotion:'reduce'});
      const page = await context.newPage(); page.setDefaultTimeout(5000);
      const errors = [], posted = [];
      let configState = 'ready';
      page.on('pageerror', e => errors.push(e.message));
      const hosts = [{id:'a',name:'Fixture A',target:'user@fixture-a'},{id:'b',name:'Fixture B',target:'fixture-b'}];
      const agents = [{id:'claude',name:'Claude Code',path:'/remote/.claude/settings.json',wired:false,fields:[{key:'model',value:'magpie/api/one',options:[{value:'magpie/api/one',label:'One'},{value:'magpie/api/two',label:'Two'}]}]}];
      let resolveConnect;
      const held = new Promise(resolve => { resolveConnect = resolve; });
      await page.route('**/*', async route => {
        const req = route.request(), url = new URL(req.url());
        const json = data => route.fulfill({json:data});
        if(url.pathname === '/boot.js') return route.fulfill({contentType:'text/javascript',body:`window.bootPrefs={lang:${JSON.stringify(lang)},theme:'light',web:false};`});
        if(url.pathname === '/wails/runtime.js') return route.fulfill({contentType:'text/javascript',body:'export const Window = {};'});
        if(url.pathname === '/api/state') return json({agents:[],profiles:[],settings:{lang,theme:'light'}});
        if(url.pathname === '/api/providers') return json({providers:[],gateway:{running:true,window:true}});
        if(url.pathname === '/api/groups') return json({groups:[]});
        if(req.method() !== 'GET') posted.push([url.pathname, req.postDataJSON()]);
        if(url.pathname === '/api/ssh/config-hosts') {
          if(configState === 'error') return route.fulfill({status:500,body:'fixture read failure'});
          return json(configState === 'empty' ? [] : ['fixture-config','other-config']);
        }
        if(url.pathname === '/api/ssh/hosts') {
          if(req.method() === 'GET') return json(hosts);
          const h = {...req.postDataJSON(),id:req.postDataJSON().id || 'added'};
          const i = hosts.findIndex(v => v.id === h.id);
          if(i < 0) hosts.push(h); else hosts[i] = h;
          return json(h);
        }
        if(url.pathname === '/api/ssh/connections') {
          await held;
          return json({id:'snapshot-a',host:hosts.find(h => h.id === req.postDataJSON().host),inspection:{installed:true,compatible:true,ready:true,service:{Installed:true,Running:true},context:{hostname:'remote-a',user:'fixture-user',os:'linux',arch:'amd64',version:'fixture',config:'/remote/magpie',gateway:'http://127.0.0.1:3425'}}});
        }
        if(url.pathname.endsWith('/agents')) return json(agents);
        if(url.pathname.endsWith('/pick')) { agents[0].fields[0].value=req.postDataJSON().value; return json({agents}); }
        if(url.pathname.endsWith('/tunnel')) return json({provider:'ssh-fixture',port:3431});
        if(url.pathname === '/api/ssh/copy-choices') return json({providers:[{id:'api',name:'API fixture'}],groups:[]});
        if(url.pathname.endsWith('/copy/preview')) return json({id:'operation-fixture',ids:{api:'api-ssh'},keys:req.postDataJSON().keys});
        if(url.pathname.endsWith('/copy/apply')) return json({ids:{api:'api-ssh'}});
        if(url.pathname.startsWith('/api/')) return json({});
        if(url.host !== 'magpie.test') return route.fulfill({status:404,body:''});
        const file = path.join(assets,url.pathname === '/' ? 'index.html' : url.pathname);
        try { await route.fulfill({body:await fs.readFile(file),contentType:{'.html':'text/html','.js':'text/javascript','.css':'text/css','.svg':'image/svg+xml'}[path.extname(file)]}); }
        catch { await route.fulfill({status:404,body:''}); }
      });
      await page.goto('http://magpie.test/?view=ssh');
      const view = page.locator('#view-ssh');
      await view.getByRole('button',{name:words('Add SSH host'),exact:true}).click();
      const hostEditor = page.locator('.ssh-editor');
      assert.equal(await hostEditor.getByRole('textbox').count(),3);
      const configPicker = page.locator('#ssh-host-config');
      await configPicker.waitFor();
      await page.waitForFunction(() => !document.querySelector('#ssh-host-config').disabled);
      await configPicker.click();
      await page.getByText('fixture-config',{exact:true}).last().click();
      assert.equal(await page.locator('#ssh-host-target').inputValue(),'fixture-config');
      assert.equal(await page.locator('#ssh-host-target').isEditable(),false);
      assert.equal(await page.locator('#ssh-host-port').isDisabled(),true);
      assert.equal(await page.locator('#ssh-host-name').inputValue(),'fixture-config');
      assert.ok(await hostEditor.evaluate(v => v.scrollWidth <= v.clientWidth + 1),`${lang} ${width}: host editor fits`);
      if(process.env.ARTIFACT_DIR && lang === 'zh') await page.screenshot({animations:'disabled',path:path.join(process.env.ARTIFACT_DIR,`ssh-host-${engine}-${width}.png`)});
      await hostEditor.getByRole('button',{name:words('Save'),exact:true}).click();
      await page.waitForFunction(() => document.querySelector('#modal').hidden);
      const configured = posted.find(([p]) => p === '/api/ssh/hosts')[1];
      assert.equal(configured.target,'fixture-config');
      assert.equal(configured.port,0,'inherit the configured port instead of overriding it with 22');
      assert.equal(posted.filter(([p]) => p === '/api/ssh/connections').length,0,'saving does not connect');
      await view.getByRole('button',{name:words('Host actions'),exact:true}).click();
      await page.getByRole('menuitem',{name:words('Edit'),exact:true}).click();
      await page.waitForFunction(() => !document.querySelector('#ssh-host-config').disabled);
      assert.equal(await configPicker.innerText(),'fixture-config');
      await configPicker.click();
      await page.getByText(words('Enter manually'),{exact:true}).last().click();
      assert.equal(await page.locator('#ssh-host-target').isEditable(),true);
      assert.equal(await page.locator('#ssh-host-port').isEnabled(),true);
      await page.locator('#ssh-host-name').fill('Manual fixture');
      await page.locator('#ssh-host-target').fill('user@manual.test');
      await page.locator('#ssh-host-port').fill('2222');
      await hostEditor.getByRole('button',{name:words('Save'),exact:true}).click();
      await page.waitForFunction(() => document.querySelector('#modal').hidden);
      const manual = posted.filter(([p]) => p === '/api/ssh/hosts').at(-1)[1];
      assert.equal(manual.id,'added');
      assert.equal(manual.target,'user@manual.test');
      assert.equal(manual.port,2222);
      await view.getByRole('button',{name:words('Connect'),exact:true}).click();
      await page.waitForFunction(() => [...document.querySelectorAll('#view-ssh button')].every(b => b.disabled));
      resolveConnect();
      await view.getByRole('button',{name:words('Open remote Web'),exact:true}).waitFor();
      await view.getByRole('button',{name:words('Host actions'),exact:true}).click();
      await page.getByRole('menuitem',{name:words('Remote environment'),exact:true}).click();
      await page.locator('.ssh-editor').getByText('/remote/magpie',{exact:true}).waitFor();
      assert.ok(await page.getByText('remote-a · fixture-user · linux · amd64 · fixture').isVisible());
      await page.locator('.ssh-editor').getByRole('button',{name:words('Close'),exact:true}).click();
      await view.getByRole('button',{name:words('Open remote Web'),exact:true}).click();
      await view.getByRole('button',{name:words('Manage remote agents'),exact:true}).click();
      await view.getByRole('button',{name:'One',exact:true}).click();
      await page.getByText('Two',{exact:true}).last().click();
      await page.waitForFunction(() => document.querySelector('#view-ssh').textContent.includes('Two'));
      assert.ok(posted.some(([p,b]) => p.endsWith('/snapshot-a/pick') && b.value === 'magpie/api/two'));
      assert.equal(await page.locator('dialog[open]').count(),0,'ordinary pick needed no confirmation');
      const source = view.locator('.row.pref').filter({hasText:words('Use local gateway')});
      await source.getByRole('button',{name:words('Connect'),exact:true}).click();
      await page.locator('.ssh-editor').getByRole('button',{name:words('Connect'),exact:true}).click();
      await source.getByRole('button',{name:words('Details'),exact:true}).click();
      await page.getByText('magpie ssh-direct ssh-fixture claude <provider/model>',{exact:true}).waitFor();
      await page.locator('.ssh-editor').getByRole('button',{name:words('Close'),exact:true}).click();
      await view.locator('.row.pref').filter({hasText:words('Copy selected configuration')}).getByRole('button',{name:words('Select'),exact:true}).click();
      const modal = page.locator('#modal');
      await modal.getByText('API fixture · api',{exact:true}).click();
      assert.equal(await modal.getByRole('checkbox').last().isChecked(),false,'keys default off');
      await modal.getByRole('button',{name:words('Preview'),exact:true}).click();
      await modal.getByText('api → api-ssh',{exact:true}).waitFor();
      assert.equal(posted.filter(([p]) => p.endsWith('/copy/apply')).length,0,'preview did not apply');
      await modal.getByRole('button',{name:words('Apply'),exact:true}).click();
      await page.waitForFunction(() => document.querySelector('#modal').hidden);
      assert.equal(posted.filter(([p]) => p.endsWith('/copy/apply')).length,1);
      assert.equal(posted.find(([p]) => p.endsWith('/copy/preview'))[1].keys,false);
      assert.ok(await view.evaluate(v => v.scrollWidth <= v.clientWidth + 1),`${lang} ${width}: no horizontal overflow`);
      assert.deepEqual(errors,[],`${lang} ${width}`);
      if(process.env.ARTIFACT_DIR && lang === 'zh') await page.screenshot({animations:'disabled',path:path.join(process.env.ARTIFACT_DIR,`ssh-${engine}-${width}.png`)});
      // A built-in switch must actually invoke onDispose, discard the
      // snapshot, close its modal and release its backend connection.
      const released = page.waitForResponse(r => r.url().endsWith('/api/ssh/connections/snapshot-a') && r.request().method() === 'DELETE');
      await page.evaluate(() => applyFeatures([{id:'ssh',on:false}]));
      await released;
      await page.waitForFunction(() => document.querySelector('#modal').hidden);
      assert.ok(posted.some(([p]) => p === '/api/ssh/connections/snapshot-a'),'switching off releases the connection');
      await page.evaluate(() => { applyFeatures([{id:'ssh',on:true}]); show('ssh'); });
      await view.getByRole('button',{name:words('Connect'),exact:true}).waitFor();
      assert.equal(await view.getByRole('button',{name:words('Open remote Web'),exact:true}).count(),0,'no stale connection after enabling again');
      for (const state of ['empty','error']) {
        configState = state;
        await view.getByRole('button',{name:words('Add SSH host'),exact:true}).click();
        const note = state === 'empty' ? 'No hosts found in ~/.ssh/config. Enter a destination manually.' : 'Could not read SSH configuration. Enter a destination manually.';
        await hostEditor.getByText(words(note),{exact:true}).waitFor();
        assert.equal(await page.locator('#ssh-host-target').isEditable(),true);
        assert.equal(await page.locator('#ssh-host-port').isEnabled(),true);
        await hostEditor.getByRole('button',{name:words('Cancel'),exact:true}).click();
      }
      assert.deepEqual(errors,[],`${lang} ${width}: configuration choices`);
      await context.close();
    }
  });
}
