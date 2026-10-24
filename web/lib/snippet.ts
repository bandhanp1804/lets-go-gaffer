// A small script the manager can run in the console of their own FPL tab to get their token
// without digging through the Network panel. It reads the token from two places in that tab
// and shows it in a box to copy: the x-api-authorization header on requests the FPL page makes
// itself, and, as a fallback, token-shaped values the tab keeps in its own storage. It sends
// nothing anywhere. It is short on purpose, so it can be read before it is run.
//
// The script lives only until the page is reloaded, so the instructions must say not to reload.

export const captureSnippet = `(() => {
  const H = "x-api-authorization";
  const TOKEN = /^(Bearer\\s+)?[\\w-]+\\.[\\w-]+\\.[\\w-]+$/i; // a real token has three parts; "undefined" and "null" are not one
  let box = null;
  let fromHeader = false;
  const expiry = (t) => {
    try { return JSON.parse(atob(t.replace(/^Bearer\\s+/i, "").split(".")[1].replace(/-/g, "+").replace(/_/g, "/"))).exp * 1000; } catch (e) { return 0; }
  };
  const show = (t, how) => {
    if (!box) {
      box = document.createElement("textarea");
      Object.assign(box.style, { position: "fixed", top: "12px", left: "12px", zIndex: 2147483647, width: "420px", height: "96px" });
      document.body.appendChild(box);
    }
    box.value = t;
    box.focus();
    box.select();
    let copied = false;
    try { copied = document.execCommand("copy"); } catch (e) {}
    console.log("Gaffer: " + how + (copied ? " and copied it. Paste it into the app." : ". Press Ctrl+C (Cmd+C) to copy the highlighted text."));
  };
  const grab = (name, value) => {
    if (fromHeader || String(name).toLowerCase() !== H) return;
    const v = String(value == null ? "" : value).trim();
    if (TOKEN.test(v)) { fromHeader = true; show(v, "caught your login token on a request FPL's page made"); }
  };
  const fromHeaders = (h) => {
    if (!h) return;
    if (typeof h.get === "function") grab(H, h.get(H));
    else if (Array.isArray(h)) h.forEach((p) => grab(p[0], p[1]));
    else for (const k in h) grab(k, h[k]);
  };
  const realFetch = window.fetch;
  window.fetch = function (input, init) {
    try { fromHeaders(init && init.headers); fromHeaders(input && input.headers); } catch (e) {}
    return realFetch.apply(this, arguments);
  };
  const realSet = XMLHttpRequest.prototype.setRequestHeader;
  XMLHttpRequest.prototype.setRequestHeader = function (k, v) { try { grab(k, v); } catch (e) {} return realSet.apply(this, arguments); };

  // Fallback: token-shaped values this tab keeps in its own storage (possibly the wrong one).
  const found = [];
  const look = (key, v, depth) => {
    if (typeof v === "string") {
      const t = v.trim();
      if (TOKEN.test(t) && expiry(t) > Date.now()) found.push({ key, t });
      else if (depth < 4 && t.length < 50000 && (t[0] === "{" || t[0] === "[")) { try { look(key, JSON.parse(t), depth + 1); } catch (e) {} }
    } else if (v && typeof v === "object" && depth < 4) for (const x of Object.values(v)) look(key, x, depth + 1);
  };
  for (const store of [window.localStorage, window.sessionStorage]) {
    try { for (let i = 0; i < store.length; i++) look(store.key(i), store.getItem(store.key(i)), 0); } catch (e) {}
  }
  if (found.length > 0) {
    show(found[0].t, 'found a possible token in this tab\\'s storage (key "' + found[0].key + '"); it may not be the right one');
    console.log("Gaffer: token-shaped values in storage:", found.map((f) => f.key + " (expires " + new Date(expiry(f.t)).toLocaleTimeString() + ")").join(", "));
  }
  return "Gaffer: listening. Do NOT reload this page (that removes the script). Click Transfers or Pick Team in FPL's menu; a box with your token appears. If none does, use the by-hand method.";
})();`;
