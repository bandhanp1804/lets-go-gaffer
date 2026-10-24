// A small script the manager can run in the console of their own FPL tab to get their token
// without digging through the Network panel. It only listens: it reads the
// x-api-authorization header off the requests FPL's own page makes, shows it in a box to copy,
// and sends nothing anywhere. It is short on purpose, so it can be read before it is run.

export const captureSnippet = `(() => {
  const H = "x-api-authorization";
  let shown = false;
  const show = (t) => {
    if (shown) return;
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
  const grab = (name, value) => { if (String(name).toLowerCase() === H && value) show(String(value)); };
  const realFetch = window.fetch;
  window.fetch = function (input, init) {
    try {
      const h = (init && init.headers) || (input && input.headers);
      if (h) { if (typeof h.get === "function") grab(H, h.get(H)); else for (const k in h) grab(k, h[k]); }
    } catch (e) {}
    return realFetch.apply(this, arguments);
  };
  const realSet = XMLHttpRequest.prototype.setRequestHeader;
  XMLHttpRequest.prototype.setRequestHeader = function (k, v) { grab(k, v); return realSet.apply(this, arguments); };
  console.log("Gaffer: ready. Now refresh your Pick Team page, or click Transfers.");
})();`;
