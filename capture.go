// `ft capture-help` prints a browser console snippet that records the method,
// path, and FIELD NAMES (never values) of the dashboard-api requests the app
// makes while you perform an action (toggle an agent, save a policy, create an
// action). Paste the printed names back and the matching command is finalized.
//
// It never captures values, headers, tokens, or cookies — only the schema.
package main

import "fmt"

const captureSnippet = `// ---- paste into the dashboard's DevTools Console, then do ONE action ----
(() => {
  window.__ftcap = [];
  const names = b => {
    try {
      if (b instanceof FormData) return [...b.keys()];
      if (typeof b === 'string' && b.trim()[0] === '{') return Object.keys(JSON.parse(b));
    } catch (e) {}
    return [];
  };
  const S = XMLHttpRequest.prototype.send, O = XMLHttpRequest.prototype.open;
  XMLHttpRequest.prototype.open = function(m,u){ this.__m=m; this.__u=u; return O.apply(this,arguments); };
  XMLHttpRequest.prototype.send = function(b){
    try { if (this.__u && this.__u.includes('dashboard-api') && this.__m !== 'GET')
      window.__ftcap.push({ method:this.__m, path:this.__u.replace(/^https?:\/\/[^/]+/, ''), fields:names(b) }); } catch(e){}
    return S.apply(this,arguments);
  };
  console.log('ft capture armed — now perform ONE action in the UI, then run: copy(JSON.stringify(window.__ftcap,null,2))');
})();`

func cmdCaptureHelp() {
	fmt.Println(captureSnippet)
	fmt.Println(`
After arming it:
  1. Perform ONE action (e.g. toggle an agent off, or save a policy).
  2. In the console run:  copy(JSON.stringify(window.__ftcap, null, 2))
  3. Paste the result here. It contains only method, path and field names —
     no values, tokens, or account data.`)
}
