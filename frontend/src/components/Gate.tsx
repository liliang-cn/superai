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
import Sky from "../canvas/Sky";
import "../canvas/canvas.css";

// The door is the canvas's own sky with one pane of glass on it, so signing in
// and arriving look like the same place.
const IDLE = [
  { key: "a", busy: 0.2, rgb: [242, 165, 22] as [number, number, number] },
  { key: "b", busy: 0.15, rgb: [255, 120, 90] as [number, number, number] },
  { key: "c", busy: 0.15, rgb: [70, 180, 170] as [number, number, number] },
  { key: "d", busy: 0.15, rgb: [140, 110, 230] as [number, number, number] },
];

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
        setErr(d.error || `Sign-in failed (HTTP ${r.status})`);
        setPassword("");
        field.current?.focus();
        return;
      }
      onEnter();
    } catch {
      setErr("Cannot reach the server.");
    } finally {
      setBusy(false);
    }
  };

  return (
    <div className="cv-root gate2">
      <Sky at={new Date()} lanes={IDLE} />
      <form className="cv-glass gate2-card" onSubmit={submit}>
        <h1>SuperAI</h1>
        <p>Your hive is behind this door.</p>
        <div className="gate2-field">
          <input
            ref={field}
            type={show ? "text" : "password"}
            value={password}
            autoComplete="current-password"
            placeholder="Password"
            onChange={(e) => setPassword(e.target.value)}
          />
          <button type="button" tabIndex={-1} onClick={() => setShow((s) => !s)}>
            {show ? "Hide" : "Show"}
          </button>
        </div>
        <button className="cv-pill ink gate2-go" type="submit" disabled={busy || !password}>
          {busy ? "Checking…" : "Enter"}
        </button>
        {err && <div className="gate2-err">{err}</div>}
      </form>
    </div>
  );
}
