package remote

// The fragment is removed from browser history before pairing. The key is
// POSTed to the relay, keeping it out of request paths and referrers.
const pairPage = `<!doctype html>
<html lang="en"><meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<meta name="referrer" content="no-referrer">
<title>Connect to Wingman</title>
<style>
body{font:16px system-ui,sans-serif;margin:0;min-height:100dvh;display:grid;place-items:center;background:#131313;color:#eee}
main{max-width:28rem;padding:2rem;text-align:center}p{color:#aaa;line-height:1.5}
button{font:inherit;min-height:44px;padding:8px 24px;border:1px solid #555;border-radius:8px;background:#252525;color:inherit;cursor:pointer}
</style>
<main><h1>Connect to Wingman</h1><p id="status">Connecting to your workspace…</p><button id="retry" hidden>Retry</button></main>
<script>
const [ID, Key] = location.hash.slice(1).split(".");
history.replaceState(null, "", "/pair");
const status = document.getElementById("status"), retry = document.getElementById("retry");
async function pair() {
  retry.hidden = true;
  if (!ID || !Key) { status.textContent = "Open the pairing link printed by Wingman, or scan its QR code."; return; }
  try {
    const response = await fetch("/pair", { method: "POST", headers: { "Content-Type": "application/json" }, body: JSON.stringify({ ID, Key }) });
    if (!response.ok) throw new Error("Workspace is offline or this pairing link has expired.");
    location.replace("/");
  } catch (error) { status.textContent = error.message; retry.hidden = false; }
}
retry.onclick = pair;
pair();
</script></html>`
