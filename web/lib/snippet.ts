// A small script the manager can run in the console of their own FPL tab to get their token
// without digging through the Network panel. It reads the token from two places in that tab
// and prints it in the console: the x-api-authorization header on requests the FPL page makes
// itself, and, first, token-shaped values the tab keeps in its own storage (preferring the one
// the login library calls access_token). It sends nothing anywhere.
//
// It prints to the console and not into the page: a text box on the page can be read by browser
// extensions such as spell checkers, which may send its text to their servers.
//
// The script lives only until the page is reloaded, so the instructions must say not to reload.

export const captureSnippet = `(() => {
  const H = "x-api-authorization";
  const TOKEN = /^(Bearer\\s+)?[\\w-]+\\.[\\w-]+\\.[\\w-]+$/i; // a real token has three parts; "undefined" and "null" are not one
  let fromHeader = false;
  const expiry = (t) => {
    try { return JSON.parse(atob(t.replace(/^Bearer\\s+/i, "").split(".")[1].replace(/-/g, "+").replace(/_/g, "/"))).exp * 1000; } catch (e) { return 0; }
  };
  const give = (t, how) => {
    console.log("Gaffer: " + how + ". Your token is the line below (treat it like a password):");
    console.log(t);
    let copied = false;
    try { if (typeof copy === "function") { copy(t); copied = true; } } catch (e) {}
    if (copied) console.log("Gaffer: it is also copied to your clipboard.");
  };
  const grab = (name, value) => {
    if (fromHeader || String(name).toLowerCase() !== H) return;
    const v = String(value == null ? "" : value).trim();
    if (TOKEN.test(v)) { fromHeader = true; give(v, "caught your login token on a request FPL's page made"); }
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

  // Token-shaped values this tab keeps in its own storage, with the name each is stored under.
  const found = [];
  const look = (path, v, depth) => {
    if (typeof v === "string") {
      const t = v.trim();
      if (TOKEN.test(t) && expiry(t) > Date.now()) found.push({ path, t });
      else if (depth < 4 && t.length < 50000 && (t[0] === "{" || t[0] === "[")) { try { look(path, JSON.parse(t), depth + 1); } catch (e) {} }
    } else if (v && typeof v === "object" && depth < 4) for (const k of Object.keys(v)) look(path + " > " + k, v[k], depth + 1);
  };
  for (const store of [window.localStorage, window.sessionStorage]) {
    try { for (let i = 0; i < store.length; i++) look(store.key(i), store.getItem(store.key(i)), 0); } catch (e) {}
  }
  // The access token is the one FPL sends; an id token or a lookalike is a worse guess.
  found.sort((a, b) => /access_token$/.test(b.path) - /access_token$/.test(a.path));
  if (found.length > 0) {
    console.log("Gaffer: token-shaped values this tab stores (names only):\\n" + found.map((f) => "  " + f.path + " (expires " + new Date(expiry(f.t)).toLocaleTimeString() + ")").join("\\n"));
    give(found[0].t, 'took the best match, "' + found[0].path + '"');
  }
  return "Gaffer: done. If the token above is rejected by the app, do NOT reload this page: click Transfers or Pick Team in FPL's menu and the real one is printed here.";
})();`;
