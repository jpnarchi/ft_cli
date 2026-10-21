// `ft capture-help` prints a browser console snippet that records the method,
// path, status and FIELD NAMES (never values) of the dashboard-api writes the
// app makes while you perform an action (save a policy, toggle an agent), plus
// the email/autopilot reads, which show which version the editor works on.
// It hooks both fetch (what the dashboard uses) and XHR.
//
// It never captures values, headers, tokens, or cookies: only field names, and
// the path with query values blanked (ids in the path, like a configuration
// id and version number, are kept because that's what the check needs).
package main

import "fmt"

const captureSnippet = `// ---- paste into the dashboard's DevTools Console, then do ONE action ----
(() => {
  window.__ftcap = [];
  const keys = v => { try {
    if (v instanceof FormData) return [...v.keys()];
    if (typeof v === 'string' && /^\s*[\[{]/.test(v)) v = JSON.parse(v);
    if (Array.isArray(v)) v = v[0];
    if (v && typeof v === 'object') return Object.keys(v);
  } catch (e) {} return []; };
  // path without the query values; ids in the path are fine to share
  const where = u => { u = String(u); let p = u; try { const x = new URL(u, location.href); p = x.pathname + x.search; } catch (e) {} const [path, q] = p.split('?');
    return q ? path + '?' + q.split('&').map(x => x.split('=')[0] + '=…').join('&') : path; };
  const log = (method, url, body, status, resp) => {
    const u = String(url);
    // writes, plus the email/autopilot reads that show which version the editor loads
    if (!u.includes('dashboard-api') || (method === 'GET' && !/email|autopilot/.test(u))) return;
    window.__ftcap.push({ method, path: where(url), fields: keys(body), status, response_fields: keys(resp) });
  };
  const F = window.fetch;
  window.fetch = async function (input, init = {}) {
    const url = typeof input === 'string' ? input : input.url;
    const method = (init.method || (input && input.method) || 'GET').toUpperCase();
    const res = await F.apply(this, arguments);
    try { res.clone().text().then(t => log(method, url, init.body, res.status, t)).catch(() => log(method, url, init.body, res.status)); } catch (e) {}
    return res;
  };
  const S = XMLHttpRequest.prototype.send, O = XMLHttpRequest.prototype.open;
  XMLHttpRequest.prototype.open = function (m, u) { this.__m = String(m).toUpperCase(); this.__u = u; return O.apply(this, arguments); };
  XMLHttpRequest.prototype.send = function (b) {
    this.addEventListener('loadend', () => { try { log(this.__m, this.__u, b, this.status, this.responseText); } catch (e) {} });
    return S.apply(this, arguments);
  };
  console.log('ft capture armed — do ONE action in the UI, then run: copy(JSON.stringify(window.__ftcap,null,2))');
})();`

func cmdCaptureHelp() {
	fmt.Println(captureSnippet)
	fmt.Println(`
After arming it:
  1. Perform ONE action (e.g. toggle an agent off, or save a policy).
  2. In the console run:  copy(JSON.stringify(window.__ftcap, null, 2))
  3. Paste the result here. It contains only method, path, status and field
     names: no values, tokens, or account data.`)
}
