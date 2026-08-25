// The password box in front of the app.
//
// SuperAI in serve mode is one person's agent reachable over the internet, and
// it has shell tools, a workspace and a billing account behind it. Something
// has to stand at the door.
//
// It used to be HTTP Basic — the browser's own popup, no page to build. That
// is the cheapest gate to write and the worst one to live behind: it looks
// like a phishing dialog, it cannot say the product's name, browsers cache it
// in ways that make signing out roughly impossible, and on iOS it arrives
// before the page paints so the site appears to be broken. This is a form,
// like SuperLeo's.
//
// It talks to the server directly rather than through the generated bindings.
// That is the one deliberate exception to "no fetch() in the frontend": the
// RPC surface is exactly what is locked, so sign-in cannot go through it, and
// in the desktop app none of this exists — a window belongs to whoever is
// sitting at the machine.

import { useEffect, useRef, useState } from "react";

export default function Gate({ onEnter }: { onEnter: () => void }) {
  const [password, setPassword] = useState("");
  const [show, setShow] = useState(false);
  const [err, setErr] = useState("");
  const [busy, setBusy] = useState(false);
  const field = useRef<HTMLInputElement>(null);

  useEffect(() => field.current?.focus(), []);

  const submit = async (e: React.FormEvent) => {
    e.preventDefault();
    if (!password || busy) return;
    setBusy(true);
    setErr("");
    try {
      const r = await fetch("/api/login", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ password }),
      });
      const d = (await r.json().catch(() => ({}))) as { error?: string };
      if (!r.ok) {
        // The server's own words: "wrong password" and "too many attempts" are
        // different problems and a single generic failure hides which one.
        setErr(d.error || `登录失败（HTTP ${r.status}）`);
        setPassword("");
        field.current?.focus();
        return;
      }
      onEnter();
    } catch {
      setErr("连不上后端");
    } finally {
      setBusy(false);
    }
  };

  return (
    <div className="gate">
      <form className="gate-card" onSubmit={submit}>
        <div className="gate-logo">S</div>
        <div className="gate-name">SuperAI</div>
        <div className="gate-sub">输入密码继续</div>
        <div className="input-pw gate-field">
          <input
            ref={field}
            className="input"
            type={show ? "text" : "password"}
            value={password}
            autoComplete="current-password"
            placeholder="密码"
            onChange={(e) => setPassword(e.target.value)}
          />
          <button
            type="button"
            className="pw-toggle"
            tabIndex={-1}
            onClick={() => setShow((s) => !s)}
          >
            {show ? "隐藏" : "显示"}
          </button>
        </div>
        <button className="btn gate-btn" type="submit" disabled={busy || !password}>
          {busy ? "验证中…" : "进入"}
        </button>
        {err && <div className="gate-err">{err}</div>}
      </form>
    </div>
  );
}
