import React, { useEffect, useState } from "react";
import { GetSettings, SaveSettings } from "../../wailsjs/go/main/App";
import { backend } from "../../wailsjs/go/models";

function PasswordField({
  label,
  value,
  onChange,
  hint,
}: {
  label: string;
  value: string;
  onChange: (v: string) => void;
  hint?: string;
}) {
  const [show, setShow] = useState(false);
  return (
    <div className="field">
      <label>{label}</label>
      <div className="input-pw">
        <input
          className="input"
          type={show ? "text" : "password"}
          value={value}
          onChange={(e) => onChange(e.target.value)}
          placeholder="••••••••"
          autoComplete="off"
        />
        <button type="button" className="pw-toggle" onClick={() => setShow((s) => !s)}>
          {show ? "hide" : "show"}
        </button>
      </div>
      {hint && <span className="hint">{hint}</span>}
    </div>
  );
}

function TextField({
  label,
  value,
  onChange,
  placeholder,
  hint,
}: {
  label: string;
  value: string;
  onChange: (v: string) => void;
  placeholder?: string;
  hint?: string;
}) {
  return (
    <div className="field">
      <label>{label}</label>
      <input className="input" value={value} onChange={(e) => onChange(e.target.value)} placeholder={placeholder} autoComplete="off" />
      {hint && <span className="hint">{hint}</span>}
    </div>
  );
}

export default function SettingsView({ onSaved }: { onSaved: () => void }) {
  const [s, setS] = useState<backend.Settings | null>(null);
  const [saving, setSaving] = useState(false);
  const [note, setNote] = useState<{ kind: "ok" | "err"; text: string } | null>(null);

  useEffect(() => {
    GetSettings()
      .then((res) => setS(res))
      .catch((e) => setNote({ kind: "err", text: String(e?.message || e) }));
  }, []);

  const set = <K extends keyof backend.Settings>(k: K, v: backend.Settings[K]) => {
    setS((prev) => (prev ? Object.assign(new backend.Settings(prev), { [k]: v }) : prev));
  };

  const save = async () => {
    if (!s) return;
    setSaving(true);
    setNote(null);
    try {
      await SaveSettings(s);
      setNote({ kind: "ok", text: "Saved. Backend rebuilt." });
      onSaved();
    } catch (e: any) {
      setNote({ kind: "err", text: String(e?.message || e) });
    } finally {
      setSaving(false);
    }
  };

  if (!s) {
    return (
      <div className="view">
        <div className="view-header">
          <div className="view-title">Settings</div>
        </div>
        <div className="loading-row"><span className="spinner" style={{ borderTopColor: "var(--accent)" }} /> Loading settings…</div>
      </div>
    );
  }

  return (
    <div className="view">
      <div className="view-header">
        <div className="view-title">Settings</div>
        <div className="view-desc">Configure providers and runtime. Saving persists and rebuilds the backend.</div>
      </div>
      <div className="settings-scroll">
        <div className="settings-grid">
          <div className="card">
            <div className="card-title">Language Model</div>
            <div className="card-desc">OpenAI-compatible chat completions endpoint.</div>
            <TextField label="Base URL" value={s.llm_base_url} onChange={(v) => set("llm_base_url", v)} placeholder="https://api.openai.com/v1" />
            <PasswordField label="API Key" value={s.llm_key} onChange={(v) => set("llm_key", v)} />
            <TextField label="Model" value={s.llm_model} onChange={(v) => set("llm_model", v)} placeholder="gpt-4o-mini" />
          </div>

          <div className="card">
            <div className="card-title">Embeddings</div>
            <div className="card-desc">Optional. Enables RAG / vector memory when configured.</div>
            <TextField label="Base URL" value={s.embed_base_url} onChange={(v) => set("embed_base_url", v)} placeholder="https://api.openai.com/v1" />
            <PasswordField label="API Key" value={s.embed_key} onChange={(v) => set("embed_key", v)} />
            <TextField label="Model" value={s.embed_model} onChange={(v) => set("embed_model", v)} placeholder="text-embedding-3-small" />
          </div>

          <div className="card">
            <div className="card-title">Runtime</div>
            <div className="card-desc">Workspace, agent loop limits, and avatar bridge.</div>
            <TextField label="Workspace Directory" value={s.workspace_dir} onChange={(v) => set("workspace_dir", v)} placeholder="~/.superai/workspace" hint="Where the agent reads and writes deliverable files." />
            <div className="row2">
              <div className="field">
                <label>Max Rounds</label>
                <input
                  className="input"
                  type="number"
                  min={1}
                  value={s.max_rounds}
                  onChange={(e) => set("max_rounds", Number(e.target.value))}
                />
                <span className="hint">Tool-call rounds per task.</span>
              </div>
              <div className="field">
                <label>Avatar Port</label>
                <input
                  className="input"
                  type="number"
                  value={s.avatar_port}
                  onChange={(e) => set("avatar_port", Number(e.target.value))}
                />
                <span className="hint">Local SSE bridge port.</span>
              </div>
            </div>
            <div className="field">
              <label>Headless Browser</label>
              <div
                className="toggle"
                onClick={() => set("headless", !s.headless)}
                style={{ cursor: "pointer", marginTop: 2 }}
              >
                <div className={`switch${s.headless ? " on" : ""}`}><div className="knob" /></div>
                <span style={{ fontSize: 13, color: "var(--text-1)" }}>
                  {s.headless ? "Run browser without a visible window" : "Show the browser window"}
                </span>
              </div>
            </div>
          </div>

          <div className="settings-actions">
            <button className="btn" onClick={save} disabled={saving}>
              {saving ? <><span className="spinner" /> Saving…</> : "Save settings"}
            </button>
            {note && <span className={`save-note ${note.kind}`}>{note.kind === "ok" ? "✓" : "⚠"} {note.text}</span>}
          </div>
        </div>
      </div>
    </div>
  );
}
