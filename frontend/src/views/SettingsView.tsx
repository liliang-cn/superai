import React, { useEffect, useState } from "react";
import AvatarSection from "./AvatarSection";
import { AppStatus } from "../lib/types";
import {
  CLIProxyAccounts,
  CLIProxyLogin,
  CLIProxyProviders,
  CLIProxySetAccountEnabled,
  CLIProxySignOut,
  CLIProxyStatus,
  CLIProxySubmitPrompt,
  ExternalAgentsStatus,
  GetSettings,
  OpenInBrowser,
  SaveSettings,
  TestWebhook,
  ToolApprovalInfo,
} from "../../wailsjs/go/main/App";
import { BrowserOpenURL, EventsOff, EventsOn } from "../../wailsjs/runtime/runtime";
import { backend } from "../../wailsjs/go/models";
import { toast } from "../lib/toasts";

type ProxyStatus = {
  running: boolean;
  error: string;
  baseURL: string;
  authDir: string;
  config: string;
  models: string[];
};

/** One line of the approval audit log, as the backend writes it. */
type AuditEntry = {
  at: string;
  tool: string;
  allowed: boolean;
  decided_by: string;
  reason?: string;
  summary?: string;
};

type ApprovalInfo = {
  enabled: boolean;
  waitSeconds: number;
  auditPath: string;
  entries: AuditEntry[];
};

/**
 * The tail of the audit log.
 *
 * On the Settings page rather than behind a menu because it is the evidence for
 * the switch sitting above it: someone deciding whether to leave the gate on
 * wants to see what it has actually been stopping. The full record is the file
 * whose path is printed underneath — this is a window onto it, not the record.
 */
function AuditTail({ info }: { info: ApprovalInfo | null }) {
  if (!info) return null;
  return (
    <div className="field">
      <label>Recent tool decisions</label>
      {info.entries.length === 0 ? (
        <span className="hint">Nothing yet. Shell commands, installs and deletions land here.</span>
      ) : (
        <div className="audit-list">
          {/* Newest first on screen; the file itself is append-order. */}
          {[...info.entries].reverse().map((e, i) => (
            <div key={`${e.at}-${i}`} className={`audit-row${e.allowed ? "" : " denied"}`}>
              <span>{e.allowed ? "allow" : "deny "}</span>
              <span>{e.tool}</span>
              <span className="audit-cmd" title={e.summary || e.reason || ""}>
                {e.summary || e.reason || ""}
              </span>
              <span>{e.decided_by}</span>
            </div>
          ))}
        </div>
      )}
      <span className="hint">
        Full log: <code>{info.auditPath}</code> — one JSON object per line, readable without this app.
      </span>
    </div>
  );
}

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

// Only the desktop window has anywhere to go. Served, this is the tab you are
// already reading it in, so the card is not rendered at all.
const served = Boolean((window as unknown as Record<string, unknown>).superaiServed);

function OpenInBrowserCard() {
  const [origin, setOrigin] = useState("");
  const [err, setErr] = useState("");
  const [busy, setBusy] = useState(false);

  const open = async () => {
    setBusy(true);
    setErr("");
    try {
      // Each click asks for its own link. The server behind it is started once
      // and reused, but the sign-in token in the URL is spent by the page load
      // that redeems it, so a second tab needs a second one.
      const url = await OpenInBrowser();
      setOrigin(new URL(url).origin);
      BrowserOpenURL(url);
    } catch (e: any) {
      setErr(String(e?.message || e));
    } finally {
      setBusy(false);
    }
  };

  return (
    <div className="card">
      <div className="card-title">Open in Browser</div>
      <div className="card-desc">
        Serve this running app on localhost and open it in your default browser. Not a copy —
        the same conversations, the same agent, the same runs in flight, seen from a second
        window. The link signs that browser in once and then expires; the server stops when
        SuperAI quits.
      </div>
      {origin && (
        <div className="url-box" style={{ marginBottom: 14 }}>
          <span className="url-label">LOCAL</span>
          <span style={{ flex: 1 }}>{origin}</span>
        </div>
      )}
      <div className="field">
        <button className="btn" onClick={open} disabled={busy} style={{ alignSelf: "flex-start" }}>
          {busy ? (
            <><span className="spinner" /> Starting…</>
          ) : origin ? (
            "Open another tab ↗"
          ) : (
            "Open in Browser ↗"
          )}
        </button>
        {err && <span className="save-note err">⚠ {err}</span>}
      </div>
    </div>
  );
}

/**
 * The settings, in groups.
 *
 * They were one column of eight cards, which meant scrolling past the model
 * accounts to reach the workspace directory and scrolling back to save. The
 * groups are by what a person came to change, not by which struct the fields
 * live in — embeddings and the memory backend are one decision made in two
 * places, so they sit together.
 */
type SectionID = "model" | "memory" | "notifications" | "safety" | "agents" | "runtime";

const SECTIONS: { id: SectionID; label: string }[] = [
  { id: "model", label: "Model" },
  { id: "memory", label: "Memory" },
  { id: "notifications", label: "Notifications" },
  { id: "safety", label: "Safety" },
  // Next to Safety, not to Runtime: the decisions on this tab are who else
  // gets to touch this machine, which is the same question the tab before it
  // asks about SuperAI itself.
  { id: "agents", label: "External agents" },
  { id: "runtime", label: "Runtime" },
];

/** The agent CLIs SuperAI can hand a task to, in the order they are listed. */
const AGENT_CLIS: { name: string; label: string }[] = [
  { name: "claude", label: "Claude Code" },
  { name: "codex", label: "Codex" },
  { name: "gemini", label: "Gemini" },
  { name: "cursor-agent", label: "Cursor Agent" },
];

/**
 * Handing work to another agent CLI.
 *
 * The switch is not offered blind: every known CLI is listed with what the
 * doctor found, because "installed" is the first thing someone needs to know
 * and the app can answer it without being asked. What it deliberately does not
 * claim is that a listed CLI *works* — they authenticate separately, and one
 * with a dead token still prints a version quite happily. So the row says
 * where the binary is, and the note underneath says the rest is untested.
 */
function ExternalAgentsCard({
  ea,
  onChange,
  statuses,
  probing,
  onRecheck,
}: {
  ea: backend.ExternalAgents;
  onChange: (patch: Partial<backend.ExternalAgents>) => void;
  statuses: backend.ExternalAgentStatus[];
  probing: boolean;
  onRecheck: () => void;
}) {
  const roots = ea.roots ?? [];
  const binaries = ea.binaries ?? {};
  const models = ea.models ?? {};
  const found = (name: string) => statuses.find((st) => st.name === name);

  const setRoot = (i: number, v: string) => {
    const next = [...roots];
    next[i] = v;
    onChange({ roots: next });
  };
  const setPair = (key: "binaries" | "models", name: string, v: string) =>
    onChange({ [key]: { ...(key === "binaries" ? binaries : models), [name]: v } } as Partial<backend.ExternalAgents>);

  return (
    <div className="card">
      <div className="card-title">External agents</div>
      <div className="card-desc">
        Let SuperAI hand a task to an agent CLI already installed here. It spends that
        subscription, not this app's, and the CLI writes files with its own approval prompt
        bypassed — so this is off until you turn it on.
      </div>

      <div className="field">
        <label>Delegate to agent CLIs</label>
        <div className="toggle" onClick={() => onChange({ enabled: !ea.enabled })} style={{ cursor: "pointer", marginTop: 2 }}>
          <div className={`switch${ea.enabled ? " on" : ""}`}><div className="knob" /></div>
          <span style={{ fontSize: 13, color: "var(--text-1)" }}>
            {ea.enabled ? "On — SuperAI may hand work to the CLIs below" : "Off — SuperAI does every task itself"}
          </span>
        </div>
      </div>

      <div className="field">
        <label>On this machine</label>
        <div className="ea-agents">
          {AGENT_CLIS.map((cli) => {
            const st = found(cli.name);
            return (
              <div key={cli.name} className={`ea-agent${st?.installed ? "" : " missing"}`}>
                <div className="ea-agent-head">
                  <span className="ea-agent-name">{cli.label}</span>
                  <span className={`ea-dot${st?.installed ? " on" : ""}`} />
                  <span className="ea-agent-state">
                    {probing && !st ? "checking…" : st?.installed ? st.version || "installed" : "not installed"}
                  </span>
                </div>
                {st?.installed && <div className="ea-agent-path" title={st.path}>{st.path}</div>}
                {ea.enabled && (
                  <div className="ea-agent-overrides">
                    <input
                      className="input"
                      value={binaries[cli.name] ?? ""}
                      onChange={(e) => setPair("binaries", cli.name, e.target.value)}
                      placeholder={st?.installed ? "path override (optional)" : `full path to ${cli.name}`}
                      autoComplete="off"
                      spellCheck={false}
                    />
                    <input
                      className="input"
                      value={models[cli.name] ?? ""}
                      onChange={(e) => setPair("models", cli.name, e.target.value)}
                      placeholder="model (optional)"
                      autoComplete="off"
                      spellCheck={false}
                    />
                  </div>
                )}
              </div>
            );
          })}
        </div>
        <span className="hint">
          Whether each one is signed in is not checked here — that would cost a real request to
          the provider on every page load. A CLI that works in your terminal but reads as
          missing usually has a path this app never inherited; type it in above.
        </span>
        <div className="row" style={{ marginTop: 8 }}>
          <button className="btn ghost sm" onClick={onRecheck} disabled={probing}>
            {probing ? "Checking…" : "Save and re-check"}
          </button>
        </div>
      </div>

      {ea.enabled && (
        <>
          <div className="field">
            <label>Directories it may work in</label>
            <span className="hint">
              The workspace is always allowed. Add a directory to let a delegated run read and
              write there too — a repository it is meant to fix, and nothing else.
            </span>
            <div className="ea-roots">
              {roots.map((r, i) => (
                <div key={i} className="ea-root">
                  <input
                    className="input"
                    value={r}
                    onChange={(e) => setRoot(i, e.target.value)}
                    placeholder="/Users/you/code/some-repo"
                    autoComplete="off"
                    spellCheck={false}
                  />
                  <button
                    type="button"
                    className="btn ghost sm"
                    onClick={() => onChange({ roots: roots.filter((_, j) => j !== i) })}
                  >
                    Remove
                  </button>
                </div>
              ))}
            </div>
            <button
              type="button"
              className="btn ghost sm"
              style={{ alignSelf: "flex-start", marginTop: 8 }}
              onClick={() => onChange({ roots: [...roots, ""] })}
            >
              Add a directory
            </button>
          </div>

          <div className="field">
            <label>Run unattended</label>
            <div className="toggle" onClick={() => onChange({ unattended: !ea.unattended })} style={{ cursor: "pointer", marginTop: 2 }}>
              <div className={`switch${ea.unattended ? " on" : ""}`}><div className="knob" /></div>
              <span style={{ fontSize: 13, color: "var(--text-1)" }}>
                {ea.unattended
                  ? "On — a delegated run is not put to you for approval"
                  : "Off — you are asked before another agent is handed the task"}
              </span>
            </div>
            {ea.unattended && (
              <span className="hint">
                For scheduled work with nobody at the machine. Every call it lets through is
                still written to the audit log under Safety.
              </span>
            )}
          </div>

          <div className="field">
            <label>Timeout (seconds)</label>
            <input
              className="input"
              type="number"
              min={0}
              value={ea.timeout_seconds ?? 0}
              onChange={(e) => onChange({ timeout_seconds: Number(e.target.value) })}
            />
            <span className="hint">
              How long one delegated run may take. 0 uses the built-in 20 minutes — long enough
              for a real task, short enough that a CLI sitting on a login prompt gives up.
            </span>
          </div>
        </>
      )}
    </div>
  );
}

export default function SettingsView({
  onSaved,
  status,
}: {
  onSaved: () => void;
  // Only the avatar section needs it, for the port the bridge is listening on.
  status: AppStatus | null;
}) {
  const [s, setS] = useState<backend.Settings | null>(null);
  const [saving, setSaving] = useState(false);
  // Which group of settings is on screen. Not in the URL and not persisted:
  // the page is opened to change one thing, and coming back to whatever was
  // last poked at is less useful than starting where the accounts are.
  const [section, setSection] = useState<SectionID>("model");
  const [proxy, setProxy] = useState<ProxyStatus | null>(null);
  const [providers, setProviders] = useState<backend.CLIProxyProvider[]>([]);
  const [projectID, setProjectID] = useState("");
  const [login, setLogin] = useState<{ status: string; provider: string; message: string } | null>(null);
  const [pasted, setPasted] = useState("");

  const [webhookTesting, setWebhookTesting] = useState(false);
  const [webhookResult, setWebhookResult] = useState("");

  /**
   * A webhook is configured once and then relied on at 08:00 while nobody is
   * watching, which is the worst moment to find out the URL was wrong — so the
   * settings screen can prove it now. Saves first: the backend sends through
   * the notifier built from the last save, not from what is on screen.
   */
  async function testWebhook() {
    if (!s) return;
    setWebhookTesting(true);
    setWebhookResult("");
    try {
      await SaveSettings(s);
      const res = await TestWebhook();
      setWebhookResult(res === "ok" ? "Delivered." : res);
    } catch (e) {
      setWebhookResult(String(e));
    } finally {
      setWebhookTesting(false);
    }
  }

  const [accounts, setAccounts] = useState<backend.CLIProxyAccount[]>([]);
  const [confirmOut, setConfirmOut] = useState("");
  const [approval, setApproval] = useState<ApprovalInfo | null>(null);
  const [agentCLIs, setAgentCLIs] = useState<backend.ExternalAgentStatus[]>([]);
  const [probing, setProbing] = useState(false);

  const refreshAgentCLIs = () => {
    setProbing(true);
    ExternalAgentsStatus()
      .then(setAgentCLIs)
      .catch(() => setAgentCLIs([]))
      .finally(() => setProbing(false));
  };

  /**
   * The probe runs against the binary paths the backend has, which are the
   * ones from the last save — so a path just typed in has to be saved before
   * re-checking, or the button would report on the old value and look broken.
   */
  async function recheckAgentCLIs() {
    if (!s) return;
    setProbing(true);
    try {
      await SaveSettings(s);
    } catch (e: any) {
      toast.error(String(e?.message || e));
    }
    refreshAgentCLIs();
  }

  const refreshApproval = () => {
    ToolApprovalInfo(20)
      .then((res) => setApproval(res as ApprovalInfo))
      .catch(() => setApproval(null));
  };

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
      .catch((e) => toast.error(String(e?.message || e)));
    CLIProxyProviders().then(setProviders).catch(() => setProviders([]));
    refreshProxy();
    refreshApproval();
    refreshAgentCLIs();

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

  // External agents is the one nested object in Settings, so it patches rather
  // than replaces: the card edits one field at a time and must not drop the
  // rest of the section on the way.
  const setEA = (patch: Partial<backend.ExternalAgents>) => {
    setS((prev) =>
      prev
        ? Object.assign(new backend.Settings(prev), {
            external_agents: Object.assign(new backend.ExternalAgents(prev.external_agents), patch),
          })
        : prev,
    );
  };

  const save = async () => {
    if (!s) return;
    setSaving(true);
    try {
      await SaveSettings(s);
      toast.success("Saved. Backend rebuilt.");
      refreshProxy();
      // The gate is rebuilt with the service, so what it reports is only true
      // after the save has been through.
      refreshApproval();
      onSaved();
    } catch (e: any) {
      toast.error(String(e?.message || e));
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
      <div className="settings-tabs" role="tablist">
        {SECTIONS.map((t) => (
          <button
            key={t.id}
            role="tab"
            aria-selected={section === t.id}
            className={`settings-tab${section === t.id ? " active" : ""}`}
            onClick={() => setSection(t.id)}
          >
            {t.label}
          </button>
        ))}
      </div>
      <div className="settings-scroll">
        <div className="settings-grid">
          <div className="card" hidden={section !== "model"}>
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

          <div className="card" hidden={section !== "model"}>
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

          <div className="card" hidden={section !== "safety"}>
            <div className="card-title">Safety</div>
            <div className="card-desc">
              Shell commands run on this machine with your permissions — the agent workspace is
              where they start, not a boundary they are held inside.
            </div>
            <div className="field">
              <label>Ask before running commands</label>
              <div
                className="toggle"
                onClick={() => set("disable_tool_approval", !s.disable_tool_approval)}
                style={{ cursor: "pointer", marginTop: 2 }}
              >
                {/* The switch reads "on = asking", so it tracks the inverse of
                    the stored disable flag. The setting is stored as a disable
                    so that a settings file which has never heard of it — every
                    file written before this existed — still comes up gated. */}
                <div className={`switch${s.disable_tool_approval ? "" : " on"}`}><div className="knob" /></div>
                <span style={{ fontSize: 13, color: "var(--text-1)" }}>
                  {s.disable_tool_approval
                    ? "Off — shell commands, installs and deletions run without asking"
                    : `On — you are asked first; unanswered after ${
                        approval?.waitSeconds ?? 120
                      }s counts as no`}
                </span>
              </div>
              {s.disable_tool_approval && (
                <span className="hint">
                  Decisions are still written to the audit log. Turn this off only for unattended
                  runs you have decided to trust — a schedule that fires at 3am has nobody to ask.
                </span>
              )}
            </div>
            <AuditTail info={approval} />
          </div>

          <div className="card" hidden={section !== "memory"}>
            <div className="card-title">Embeddings</div>
            <div className="card-desc">Optional. Enables RAG / vector memory when configured.</div>
            <TextField label="Base URL" value={s.embed_base_url} onChange={(v) => set("embed_base_url", v)} placeholder="https://api.openai.com/v1" />
            <PasswordField label="API Key" value={s.embed_key} onChange={(v) => set("embed_key", v)} />
            <TextField label="Model" value={s.embed_model} onChange={(v) => set("embed_model", v)} placeholder="text-embedding-3-small" />
          </div>

          <div className="card" hidden={section !== "memory"}>
            <div className="card-title">Memory</div>
            <div className="card-desc">
              Where durable memory lives. Pick one — a capability should have exactly one route.
            </div>
            <div className="field">
              <label>Backend</label>
              <div style={{ display: "flex", flexWrap: "wrap", gap: 8, marginTop: 4 }}>
                {(["local", "shared"] as const).map((mode) => (
                  <button
                    key={mode}
                    type="button"
                    className={`btn sm${s.memory_backend === mode ? "" : " ghost"}`}
                    onClick={() => set("memory_backend", mode)}
                  >
                    {mode === "local" ? "Local (this machine)" : "Shared brain (remote CortexDB)"}
                  </button>
                ))}
              </div>
              <span className="hint">
                {s.memory_backend === "shared"
                  ? "memory_* tools read and write the shared CortexDB. Any MCP server pointed at the same address is left unmounted, so the model does not see two names for one store."
                  : "memory_* tools use this machine's own store. MCP servers are mounted as configured."}
              </span>
            </div>
            {s.memory_backend === "shared" && (
              <>
                <TextField
                  label="Shared CortexDB Endpoint"
                  value={s.shared_memory_endpoint}
                  onChange={(v) => set("shared_memory_endpoint", v)}
                  placeholder="192.168.1.10:47821"
                  hint="host:port of the cortexdb gRPC server. Empty falls back to $CORTEXDB_REMOTE."
                />
                <PasswordField
                  label="Shared CortexDB Token"
                  value={s.shared_memory_token}
                  onChange={(v) => set("shared_memory_token", v)}
                />
                <span className="hint">Leave the token empty to read $CORTEXDB_GRPC_TOKEN instead, keeping it out of settings.json.</span>
                <TextField
                  label="Namespace"
                  value={s.shared_memory_namespace}
                  onChange={(v) => set("shared_memory_namespace", v)}
                  placeholder="default"
                  hint="Scopes reads and writes inside the shared brain."
                />
              </>
            )}
          </div>

          <div className="card" hidden={section !== "notifications"}>
            <div className="card-title">Notifications</div>
            <div className="card-desc">
              Where messages go when nobody is looking at this page. notify_user and every
              scheduled run are POSTed as JSON, so Telegram, a WeCom bot, bark, ntfy or a
              script of your own can all receive them behind a short adapter.
            </div>
            <TextField
              label="Webhook URL"
              value={s.webhook_url}
              onChange={(v) => set("webhook_url", v)}
              placeholder="https://example.com/superai-hook"
              hint="Empty disables it. Serve mode has no desktop notification, so without this a reminder that fires reaches only the log."
            />
            <PasswordField
              label="Webhook Secret"
              value={s.webhook_secret}
              onChange={(v) => set("webhook_secret", v)}
            />
            <span className="hint">
              Sent as <code>Authorization: Bearer &lt;secret&gt;</code>. Optional, but worth setting
              if the receiver is reachable from the internet.
            </span>
            <div className="row">
              <button className="btn" onClick={testWebhook} disabled={webhookTesting || !s.webhook_url}>
                {webhookTesting ? "Sending…" : "Send test"}
              </button>
              {webhookResult && <span className="hint">{webhookResult}</span>}
            </div>
          </div>

          {section === "agents" && (
            <ExternalAgentsCard
              ea={s.external_agents ?? new backend.ExternalAgents()}
              onChange={setEA}
              statuses={agentCLIs}
              probing={probing}
              onRecheck={recheckAgentCLIs}
            />
          )}

          <div className="card" hidden={section !== "runtime"}>
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

          {/* Folded in from what used to be its own sidebar entry. It sits
              after Runtime, and in the same section, because that is where the
              avatar port it depends on is set. */}
          {section === "runtime" && <AvatarSection status={status} />}

          {!served && section === "runtime" && <OpenInBrowserCard />}

          <div className="settings-actions">
            <button className="btn" onClick={save} disabled={saving}>
              {saving ? <><span className="spinner" /> Saving…</> : "Save settings"}
            </button>
          </div>
        </div>
      </div>
    </div>
  );
}
