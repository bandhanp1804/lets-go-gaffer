// A small script the manager can run in the console of their own FPL tab to get their token
// without digging through the Network panel. It only listens: it reads the
// x-api-authorization header off the requests FPL's own page makes, shows it in a box to copy,
// and sends nothing anywhere. It is short on purpose, so it can be read before it is run.

export const captureSnippet = `(() => {
  const H = "x-api-authorization";
  const TOKEN = /^(Bearer\\s+)?[\\w-]+\\.[\\w-]+\\.[\\w-]+$/i; // a real token has three parts; "undefined" and "null" are not one
  let shown = false;
  const show = (t) => {
    shown = true;
    const box = document.createElement("textarea");
    box.value = t;
    Object.assign(box.style, { position: "fixed", top: "12px", left: "12px", zIndex: 2147483647, width: "420px", height: "96px" });
    document.body.appendChild(box);
    box.focus();
    box.select();
    let copied = false;
    try { copied = document.execCommand("copy"); } catch (e) {}
    console.log(copied ? "Gaffer: token copied. Paste it into the app." : "Gaffer: press Ctrl+C (Cmd+C) to copy the highlighted token.");
  };
  const grab = (name, value) => {
    if (shown || String(name).toLowerCase() !== H) return;
    const v = String(value == null ? "" : value).trim();
    if (TOKEN.test(v)) show(v);
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
  return "Gaffer: listening. Now refresh your Pick Team page (or click Transfers). If no box appears, use the by-hand method instead.";
})();`;
