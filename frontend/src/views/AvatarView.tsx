import React, { useState } from "react";
import { BrowserOpenURL, ClipboardSetText } from "../../wailsjs/runtime";
import { EmitAvatarTest } from "../../wailsjs/go/main/App";
import { AppStatus } from "../lib/types";

const EMOTIONS: { key: string; emoji: string }[] = [
  { key: "happy", emoji: "😄" },
  { key: "sad", emoji: "😢" },
  { key: "thinking", emoji: "🤔" },
  { key: "excited", emoji: "🤩" },
  { key: "neutral", emoji: "😐" },
];

export default function AvatarView({ status }: { status: AppStatus | null }) {
  const port = status?.avatarPort && status.avatarPort > 0 ? status.avatarPort : 0;
  const base = port ? `http://127.0.0.1:${port}` : "http://127.0.0.1:<port>";
  const eventsUrl = `${base}/avatar/events`;
  const pageUrl = `${base}/avatar`;
  const [fired, setFired] = useState<string>("");
  const [copied, setCopied] = useState(false);

  const test = async (emotion: string) => {
    try {
      await EmitAvatarTest(emotion);
      setFired(emotion);
      setTimeout(() => setFired(""), 1200);
    } catch {
      /* ignore */
    }
  };

  const copy = () => {
    ClipboardSetText(eventsUrl).then(() => {
      setCopied(true);
      setTimeout(() => setCopied(false), 1200);
    });
  };

  return (
    <div className="view">
      <div className="view-header">
        <div className="view-title">Avatar Bridge</div>
        <div className="view-desc">Stream emotions to an external 2D / 3D avatar renderer over SSE.</div>
      </div>
      <div className="avatar-scroll">
        <div className="avatar-grid">
          <div className="card">
            <div className="card-title">SSE Event Stream</div>
            <div className="card-desc">
              External renderers connect to this URL to receive live emotion events.
              {!port && " Set an avatar port in Settings to activate."}
            </div>
            <div className="url-box">
              <span className="url-label">GET</span>
              <span style={{ flex: 1 }}>{eventsUrl}</span>
              <button className="btn ghost sm" onClick={copy} disabled={!port}>{copied ? "Copied!" : "Copy"}</button>
            </div>
          </div>

          <div className="card">
            <div className="card-title">Reference Renderer</div>
            <div className="card-desc">A built-in reference page that visualizes the avatar event stream.</div>
            <div className="url-box">
              <span className="url-label">PAGE</span>
              <span style={{ flex: 1 }}>{pageUrl}</span>
              <button className="btn sm" onClick={() => port && BrowserOpenURL(pageUrl)} disabled={!port}>Open ↗</button>
            </div>
          </div>

          <div className="card">
            <div className="card-title">Emotion Test</div>
            <div className="card-desc">Emit a test emotion event onto the stream to verify your renderer.</div>
            <div className="emotion-btns">
              {EMOTIONS.map((e) => (
                <button
                  key={e.key}
                  className="emotion-btn"
                  onClick={() => test(e.key)}
                  style={fired === e.key ? { borderColor: "var(--accent)", background: "var(--accent-soft)" } : undefined}
                >
                  <span className="em-emoji">{e.emoji}</span>
                  <span className="em-label">{e.key}</span>
                </button>
              ))}
            </div>
            {fired && <div style={{ marginTop: 12, fontSize: 12, color: "var(--green)" }}>✓ Emitted "{fired}"</div>}
          </div>
        </div>
      </div>
    </div>
  );
}
