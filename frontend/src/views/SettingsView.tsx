import React, { useEffect, useState } from "react";
import {
  CLIProxyAccounts,
  CLIProxyLogin,
  CLIProxyProviders,
  CLIProxySetAccountEnabled,
  CLIProxySignOut,
  CLIProxyStatus,
  CLIProxySubmitPrompt,
  GetSettings,
  SaveSettings,
} from "../../wailsjs/go/main/App";
import { EventsOff, EventsOn } from "../../wailsjs/runtime/runtime";
import { backend } from "../../wailsjs/go/models";

type ProxyStatus = {
  running: boolean;
  error: string;
  baseURL: string;
  authDir: string;
  config: string;
  models: string[];
};

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
  const [proxy, setProxy] = useState<ProxyStatus | null>(null);
  const [providers, setProviders] = useState<backend.CLIProxyProvider[]>([]);
  const [projectID, setProjectID] = useState("");
  const [login, setLogin] = useState<{ status: string; provider: string; message: string } | null>(null);
  const [pasted, setPasted] = useState("");

  const [accounts, setAccounts] = useState<backend.CLIProxyAccount[]>([]);
  const [confirmOut, setConfirmOut] = useState("");

  const refreshProxy = () => {
    CLIProxyStatus()
      .then((res) => setProxy(res as ProxyStatus))
      .catch(() => setProxy(null));
    CLIProxyAccounts()
      .then(setAccounts)
      .catch(() => setAccounts([]));
  };

  useEffect(() => {
    GetSettings()
      .then((res) => setS(res))
      .catch((e) => setNote({ kind: "err", text: String(e?.message || e) }));
    CLIProxyProviders().then(setProviders).catch(() => setProviders([]));
    refreshProxy();

    EventsOn("cliproxy:login", (ev: any) => {
      setLogin(ev);
      if (ev?.status === "done") {
        setPasted("");
        // The proxy hot-loads the new credential; give its watcher a moment.
        setTimeout(refreshProxy, 1200);
      }
    });
    return () => EventsOff("cliproxy:login");
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
      refreshProxy();
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
            <div className="card-title">Accounts</div>
            <div className="card-desc">
              Sign in with the AI accounts you already pay for — no API key needed. Everything stays on this machine.
            </div>
            <div className="field">
              <label>Use my accounts</label>
              <div
                className="toggle"
                onClick={() => set("cliproxy_enabled", !s.cliproxy_enabled)}
                style={{ cursor: "pointer", marginTop: 2 }}
              >
                <div className={`switch${s.cliproxy_enabled ? " on" : ""}`}><div className="knob" /></div>
                <span style={{ fontSize: 13, color: "var(--text-1)" }}>
                  {s.cliproxy_enabled
                    ? "On — SuperAI runs on your signed-in accounts"
                    : "Off — SuperAI uses the API endpoint set under Advanced"}
                </span>
              </div>
            </div>
            {s.cliproxy_enabled && (
              <div className="field">
                <label>Model</label>
                {proxy?.running ? (
                  <span className="hint">
                    {proxy.models?.length
                      ? `${proxy.models.length} models available from your accounts — pick the one SuperAI should think with.`
                      : "No accounts yet — sign in below to see the models you can use."}
                  </span>
                ) : (
                  <span className="hint">
                    {proxy?.error ? proxy.error : "Sign in below to get started."}
                  </span>
                )}
                {proxy?.running && !!proxy.models?.length && (
                  <div style={{ display: "flex", flexWrap: "wrap", gap: 8, marginTop: 8 }}>
                    {proxy.models.map((m) => (
                      <button
                        key={m}
                        type="button"
                        className={`btn sm${s.llm_model === m ? "" : " ghost"}`}
                        onClick={() => set("llm_model", m)}
                      >
                        {m}
                      </button>
                    ))}
                  </div>
                )}
              </div>
            )}

            {s.cliproxy_enabled && !!accounts.length && (
              <div className="field">
                <label>Signed in</label>
                <span className="hint">
                  Pause an account to hide its models without signing out.
                </span>
                <div style={{ display: "flex", flexDirection: "column", gap: 8, marginTop: 8 }}>
                  {accounts.map((acc) => (
                    <div
                      key={acc.file}
                      style={{ display: "flex", alignItems: "center", gap: 10, flexWrap: "wrap" }}
                    >
                      <span style={{ fontSize: 13, color: acc.disabled ? "var(--text-2)" : "var(--text-0)" }}>
                        <b>{providers.find((p) => p.id === acc.provider)?.label || acc.provider}</b> · {acc.account}
                        {acc.project ? ` · ${acc.project}` : ""}
                        {acc.disabled ? " (paused)" : ""}
                      </span>
                      <button
                        type="button"
                        className="btn ghost sm"
                        onClick={() =>
                          CLIProxySetAccountEnabled(acc.file, acc.disabled).then(() => setTimeout(refreshProxy, 800))
                        }
                      >
                        {acc.disabled ? "Resume" : "Pause"}
                      </button>
                      <button
                        type="button"
                        className="btn ghost sm"
                        onClick={() => {
                          if (confirmOut !== acc.file) {
                            setConfirmOut(acc.file);
                            return;
                          }
                          setConfirmOut("");
                          CLIProxySignOut(acc.file).then(() => setTimeout(refreshProxy, 800));
                        }}
                      >
                        {confirmOut === acc.file ? "Click again to delete" : "Sign out"}
                      </button>
                    </div>
                  ))}
                </div>
              </div>
            )}

            {s.cliproxy_enabled && (
              <div className="field">
                <label>Add an account</label>
                <span className="hint">Opens your browser. Sign-in is stored on this machine only.</span>
                <div style={{ display: "flex", flexWrap: "wrap", gap: 8, marginTop: 8 }}>
                  {providers.map((prov) => (
                    <button
                      key={prov.id}
                      type="button"
                      className="btn ghost sm"
                      title={prov.note}
                      disabled={login?.status === "started" || login?.status === "prompt"}
                      onClick={() => {
                        setLogin({ status: "started", provider: prov.id, message: "Starting…" });
                        CLIProxyLogin(prov.id, prov.needs_project ? projectID : "").then(() => refreshProxy());
                      }}
                    >
                      Sign in with {prov.label}
                    </button>
                  ))}
                </div>
                {providers.some((p) => p.needs_project) && (
                  <input
                    className="input"
                    style={{ marginTop: 8 }}
                    value={projectID}
                    onChange={(e) => setProjectID(e.target.value)}
                    placeholder="Google Cloud project ID (Gemini only)"
                    autoComplete="off"
                  />
                )}
                {login && (
                  <span className={`hint${login.status === "error" ? " err" : ""}`} style={{ marginTop: 6 }}>
                    {providers.find((p) => p.id === login.provider)?.label || login.provider}: {login.message}
                  </span>
                )}
                {login?.status === "prompt" && (
                  <div className="input-pw" style={{ marginTop: 6 }}>
                    <input
                      className="input"
                      value={pasted}
                      onChange={(e) => setPasted(e.target.value)}
                      placeholder="Paste the callback URL from your browser"
                      autoComplete="off"
                    />
                    <button
                      type="button"
                      className="pw-toggle"
                      onClick={() => {
                        CLIProxySubmitPrompt(pasted);
                        setLogin({ ...login, status: "started", message: "Submitted, finishing sign-in…" });
                      }}
                    >
                      submit
                    </button>
                  </div>
                )}
              </div>
            )}
          </div>

          <div className="card">
            <div className="card-title">Advanced</div>
            <div className="card-desc">
              {s.cliproxy_enabled
                ? "Only used when \"Use my accounts\" is off — or to force a model name by hand."
                : "OpenAI-compatible chat completions endpoint."}
            </div>
            <TextField label="API Base URL" value={s.llm_base_url} onChange={(v) => set("llm_base_url", v)} placeholder="https://api.openai.com/v1" />
            <PasswordField label="API Key" value={s.llm_key} onChange={(v) => set("llm_key", v)} />
            <TextField label="Model" value={s.llm_model} onChange={(v) => set("llm_model", v)} placeholder="gpt-5.5" />
            <div className="field">
              <label>Local Port</label>
              <input
                className="input"
                type="number"
                value={s.cliproxy_port}
                onChange={(e) => set("cliproxy_port", Number(e.target.value))}
              />
              <span className="hint">Only change this if something else already uses it.</span>
            </div>
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
            <div className="field">
              <label>Disable PTC (programmatic tool calling)</label>
              <div
                className="toggle"
                onClick={() => set("disable_ptc", !s.disable_ptc)}
                style={{ cursor: "pointer", marginTop: 2 }}
              >
                <div className={`switch${s.disable_ptc ? " on" : ""}`}><div className="knob" /></div>
                <span style={{ fontSize: 13, color: "var(--text-1)" }}>
                  {s.disable_ptc
                    ? "Direct one-tool-per-round calling (needed for DashScope qwen3.x)"
                    : "PTC on — model writes JS that calls tools (best with gpt-5.x)"}
                </span>
              </div>
            </div>
            <div className="field">
              <label>PII redaction (strip personal data before sending to the LLM)</label>
              <div
                className="toggle"
                onClick={() => set("pii_redaction", !s.pii_redaction)}
                style={{ cursor: "pointer", marginTop: 2 }}
              >
                <div className={`switch${s.pii_redaction ? " on" : ""}`}><div className="knob" /></div>
                <span style={{ fontSize: 13, color: "var(--text-1)" }}>
                  {s.pii_redaction
                    ? "On — email / phone / 身份证 / 手机号 / 银行卡 masked before reaching the model (cloud-safe)"
                    : "Off — the model sees your data as-is (needed when it must act on real PII)"}
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
