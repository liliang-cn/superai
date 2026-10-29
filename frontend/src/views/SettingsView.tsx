import React, { useEffect, useState } from "react";
import { translate, useI18n } from "../lib/i18n";
import LanguageSettings from "../components/LanguageSettings";
import PairPhoneCard from "../components/PairPhoneCard";
import LinkSuperLeoCard from "../components/LinkSuperLeoCard";
import LocalHiveCard from "../components/LocalHiveCard";
import QueenEngineCard from "../components/QueenEngineCard";
import ProxyCard from "../components/ProxyCard";
import BrowserModeCard from "../components/BrowserModeCard";
import DesktopControlCard from "../components/DesktopControlCard";
import AddAgentCard from "../components/AddAgentCard";
import AddWorkerCard from "../components/AddWorkerCard";
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
  CheckRemoteAgents,
  SaveSettings,
  TestWebhook,
  ToolApprovalInfo,
} from "../../wailsjs/go/app/App";
import { BrowserOpenURL, EventsOff, EventsOn } from "../../wailsjs/runtime/runtime";
import { app, backend } from "../../wailsjs/go/models";
import { toast } from "../lib/toasts";
import { DEFAULT_QUEEN, setQueenName } from "../lib/queenName";

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
        <span className="hint">{translate("None")}</span>
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
      <span className="hint"><code>{info.auditPath}</code></span>
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
      <label>{translate(label)}</label>
      <div className="input-pw">
        <input
          className="input"
          type={show ? "text" : "password"}
          value={value}
          aria-label={translate(label)}
          onChange={(e) => onChange(e.target.value)}
          placeholder="••••••••"
          autoComplete="off"
        />
        <button type="button" className="pw-toggle" onClick={() => setShow((s) => !s)}>
          {translate(show ? "hide" : "show")}
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
      <label>{translate(label)}</label>
      <input className="input" aria-label={translate(label)} value={value} onChange={(e) => onChange(e.target.value)} placeholder={placeholder} autoComplete="off" />
      {hint && <span className="hint">{hint}</span>}
    </div>
  );
}

/**
 * The queen's name and aliases. Display and address only: the role and the
 * agent stay "queen". Aliases are typed as one comma-separated line, so the
 * text is kept as typed and split on the way into the settings.
 */
function QueenNameCard({ name, aliases, onChange, hidden }: {
  hidden: boolean;
  name: string;
  aliases: string[];
  onChange: (name: string, aliases: string[]) => void;
}) {
  const [text, setText] = useState(aliases.join(", "));
  useEffect(() => {
    const parsed = text.split(/[,，\s]+/).filter(Boolean);
    if (parsed.join() !== aliases.join()) setText(aliases.join(", "));
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [aliases.join()]);
  return (
    <div className="card" hidden={hidden}>
      <div className="card-title">{translate("Queen's name")}</div>
      <TextField label="Name" value={name} placeholder={DEFAULT_QUEEN} onChange={(v) => onChange(v, aliases)} />
      <div className="field">
        <label>{translate("Also answers to")}</label>
        <input className="input" aria-label={translate("Also answers to")} value={text} autoComplete="off"
          placeholder={translate("Comma-separated, e.g. bee, 小蜂")}
          onChange={(e) => { setText(e.target.value); onChange(name, e.target.value.split(/[,，\s]+/).filter(Boolean)); }} />
      </div>
    </div>
  );
}

// Only the desktop window has anywhere to go. Served, this is the tab you are
// already reading it in, so the card is not rendered at all.
const served = Boolean((window as unknown as Record<string, unknown>).superleoServed);

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
 * The settings, in three groups: what it thinks and remembers with, what it
 * can reach and who decides, and everything else. Embeddings and the memory
 * backend are one decision made in two places, so they sit together; the hive
 * is an ability like the browser, so it sits with the browser.
 */
type SectionID = "model" | "abilities" | "general";

const SECTIONS: { id: SectionID; label: string }[] = [
  { id: "model", label: "Model" },
  { id: "abilities", label: "Abilities" },
  { id: "general", label: "General" },
];

/** The agent CLIs SuperLeo can hand a task to, in the order they are listed. */
const AGENT_CLIS: { name: string; label: string }[] = [
  { name: "claude", label: "Claude Code" },
  { name: "codex", label: "Codex" },
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
  reached,
  probing,
  onRecheck,
}: {
  ea: backend.ExternalAgents;
  onChange: (patch: Partial<backend.ExternalAgents>) => void;
  statuses: backend.ExternalAgentStatus[];
  /** What each CLI said when it was last actually asked something, if ever. */
  reached: app.RemoteAgentStatus[];
  probing: boolean;
  onRecheck: () => void;
}) {
  const roots = ea.roots ?? [];
  const binaries = ea.binaries ?? {};
  const models = ea.models ?? {};
  const found = (name: string) => statuses.find((st) => st.name === name);
  const answered = (name: string) => reached.find((st) => st.name === name);

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

      <div className="field">
        <label>Delegate to agent CLIs</label>
        <div className="toggle" onClick={() => onChange({ enabled: !ea.enabled })} style={{ cursor: "pointer", marginTop: 2 }}>
          <div className={`switch${ea.enabled ? " on" : ""}`}><div className="knob" /></div>
          <span style={{ fontSize: 13, color: "var(--text-1)" }}>
            {ea.enabled ? "On — SuperLeo may hand work to the CLIs below" : "Off — SuperLeo does every task itself"}
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
                {/* The other half of "installed". A CLI whose token was
                    revoked prints its refusal as an answer and exits 0, so
                    only having asked it something tells you anything — and
                    when it refuses, its own words name the cause. */}
                {(() => {
                  const ans = answered(cli.name);
                  if (!ans) return null;
                  return ans.reachable ? (
                    <span className="hint">answered in {(ans.ms / 1000).toFixed(1)}s</span>
                  ) : (
                    <span className="hint ea-bad">did not answer — {ans.detail}</span>
                  );
                })()}
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
                    {translate("Remove")}
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
            <span className="hint">0 = 20 min</span>
          </div>
        </>
      )}
    </div>
  );
}


/**
 * The agents on other machines, and one button that finds out whether any of
 * them actually answers.
 *
 * The check asks a real question rather than looking for a binary or a port,
 * because everything this feature has taught so far is that reachable and
 * usable are different: a CLI with a revoked token, a node that no longer
 * holds the volume and a model whose tier was withdrawn all look healthy right
 * up until something is asked. It costs a token per agent, so it is a button.
 */
function RemoteAgentsCard({
  ra,
  onChange,
  statuses,
  checking,
  onCheck,
}: {
  ra: backend.RemoteAgents;
  onChange: (patch: Partial<backend.RemoteAgents>) => void;
  statuses: app.RemoteAgentStatus[];
  checking: boolean;
  onCheck: () => void;
}) {
  const configured = Object.entries(ra.agents ?? {});
  const stateOf = (name: string) => statuses.find((st) => st.name === name);

  return (
    <div className="card">
      <div className="card-title">Agents on other machines</div>

      <div className="field">
        <label>Reach agents over the network</label>
        <div className="toggle" onClick={() => onChange({ enabled: !ra.enabled })} style={{ cursor: "pointer", marginTop: 2 }}>
          <div className={`switch${ra.enabled ? " on" : ""}`}><div className="knob" /></div>
          <span style={{ fontSize: 13, color: "var(--text-1)" }}>
            {ra.enabled ? "On — @pi and the rest resolve, here and in chat" : "Off — only the CLIs on this machine"}
          </span>
        </div>
      </div>

      {configured.length > 0 && (
        <div className="field">
          <label>Configured</label>
          <div className="ea-agents">
            {configured.map(([name, a]) => {
              const st = stateOf(name);
              return (
                <div key={name} className={`ea-agent${st && !st.reachable ? " missing" : ""}`}>
                  <div className="ea-agent-head">
                    <span className="ea-agent-name">@{name}</span>
                    <span className="ea-agent-state">
                      {checking && !st
                        ? "asking…"
                        : st
                          ? st.reachable
                            ? `answered from ${st.where} in ${(st.ms / 1000).toFixed(1)}s`
                            : "no answer"
                          : "not checked"}
                    </span>
                  </div>
                  <div className="hint ra-about">{a.about}</div>
                  {/* The reason, verbatim. Every failure this has produced so
                      far named its own cause — a revoked token, a withdrawn
                      tier, a config read from the wrong directory — and
                      summarising them would have thrown that away. */}
                  {st && !st.reachable && st.detail && <span className="hint ea-bad">{st.detail}</span>}
                  {/* The nodes it may be on, in the order they are tried.
                      Monospaced because half of them are ssh options. */}
                  <div className="ea-agent-path">{(a.hosts ?? []).join("   ·   ")}</div>
                </div>
              );
            })}
          </div>
        </div>
      )}

      <div className="row" style={{ marginTop: 8 }}>
        <button className="btn ghost sm" onClick={onCheck} disabled={checking}>
          {checking ? "Asking every agent…" : "Save and ask each one"}
        </button>
      </div>
    </div>
  );
}

export default function SettingsView({ onSaved }: { onSaved: () => void }) {
  const { t } = useI18n();
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
  // Kept apart from the CLI probe above: that one looks for a binary and takes
  // milliseconds, this one asks every agent a question and takes seconds.
  const [reachable, setReachable] = useState<app.RemoteAgentStatus[]>([]);
  const [checking, setChecking] = useState(false);

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

  /**
   * Save, then actually ask every agent something.
   *
   * Saving first is the point: the check is only worth anything against the
   * settings that will be in force, and a toggle flipped but not saved would
   * have the button report on the old configuration.
   */
  async function checkReachable() {
    if (!s) return;
    setChecking(true);
    setReachable([]);
    try {
      await SaveSettings(s);
      setReachable((await CheckRemoteAgents()) as app.RemoteAgentStatus[]);
    } catch (e: any) {
      toast.error(String(e?.message || e));
    } finally {
      setChecking(false);
    }
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
  const setRA = (patch: Partial<backend.RemoteAgents>) => {
    setS((prev) =>
      prev
        ? Object.assign(new backend.Settings(prev), {
            remote_agents: Object.assign(new backend.RemoteAgents(prev.remote_agents), patch),
          })
        : prev,
    );
  };

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
      setQueenName(s.queen_name, s.queen_aliases);
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
          <div className="view-title">{t("Settings")}</div>
        </div>
        <div className="loading-row"><span className="spinner" style={{ borderTopColor: "var(--accent)" }} /> Loading settings…</div>
      </div>
    );
  }

  return (
    <div className="view">
      <div className="view-header with-action">
        <div className="view-title">{t("Settings")}</div>
        <div className="vh-actions"><LanguageSettings /></div>
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
            {translate(t.label)}
          </button>
        ))}
      </div>
      <div className="settings-scroll">
        <div className="settings-grid">
          {/* ── Model: what it thinks and remembers with ── */}
          <div className="card" hidden={section !== "model"}>
            <div className="card-title">{translate("Accounts")}</div>
            <div className="field">
              <label>{translate("Use my accounts")}</label>
              <div
                className="toggle"
                onClick={() => set("cliproxy_enabled", !s.cliproxy_enabled)}
                style={{ cursor: "pointer", marginTop: 2 }}
              >
                <div className={`switch${s.cliproxy_enabled ? " on" : ""}`}><div className="knob" /></div>
                <span style={{ fontSize: 13, color: "var(--text-1)" }}>
                  {translate(s.cliproxy_enabled ? "On — SuperLeo runs on your signed-in accounts" : "Off — SuperLeo uses the custom API")}
                </span>
              </div>
            </div>
            {s.cliproxy_enabled && (
              <div className="field">
                <label>Model</label>
                {!proxy?.running && (
                  <span className="hint">{proxy?.error ? proxy.error : "Sign in below to get started."}</span>
                )}
                {proxy?.running && !proxy.models?.length && (
                  <span className="hint">No accounts yet — sign in below.</span>
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
            <div className="card-title">{translate("Custom API")}</div>
            <TextField label="API Base URL" value={s.llm_base_url} onChange={(v) => set("llm_base_url", v)} placeholder="https://api.openai.com/v1" />
            <PasswordField label="API Key" value={s.llm_key} onChange={(v) => set("llm_key", v)} />
            <TextField label="Model" value={s.llm_model} onChange={(v) => set("llm_model", v)} placeholder="gpt-5.5" />
          </div>

          {s.hive?.role !== "worker" && (
            <QueenEngineCard
              hidden={section !== "model"}
              engine={s.hive?.queen_engine ?? ""}
              fallback={s.hive?.queen_fallback !== false}
              publicURL={s.hive?.public_url ?? ""}
              onChange={(engine, fallback, publicURL) =>
                setS((prev) =>
                  prev
                    ? Object.assign(new backend.Settings(prev), {
                        hive: new backend.HiveSettings({ ...prev.hive, queen_engine: engine, queen_fallback: fallback, public_url: publicURL }),
                      })
                    : prev,
                )
              }
            />
          )}

          <div className="card" hidden={section !== "model"}>
            <div className="card-title">{translate("Embeddings")}</div>
            <TextField label="Base URL" value={s.embed_base_url} onChange={(v) => set("embed_base_url", v)} placeholder="https://api.openai.com/v1" />
            <PasswordField label="API Key" value={s.embed_key} onChange={(v) => set("embed_key", v)} />
            <TextField label="Model" value={s.embed_model} onChange={(v) => set("embed_model", v)} placeholder="text-embedding-3-small" />
          </div>

          <div className="card" hidden={section !== "model"}>
            <div className="card-title">{translate("Memory")}</div>
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
            </div>
            {s.memory_backend === "shared" && (
              <>
                <TextField
                  label="Shared CortexDB Endpoint"
                  value={s.shared_memory_endpoint}
                  onChange={(v) => set("shared_memory_endpoint", v)}
                  placeholder="192.168.1.10:47821"
                />
                <PasswordField
                  label="Shared CortexDB Token"
                  value={s.shared_memory_token}
                  onChange={(v) => set("shared_memory_token", v)}
                />
                <TextField
                  label="Namespace"
                  value={s.shared_memory_namespace}
                  onChange={(v) => set("shared_memory_namespace", v)}
                  placeholder="default"
                />
              </>
            )}
          </div>

          {/* ── Abilities: what it can reach, and who else it can hand work to ── */}
          {/* Saved the moment it is picked, so the page's own copy of the
              settings is told too: a later Save must not put the old mode back. */}
          {section === "abilities" && (
            <BrowserModeCard onChanged={(mode) => setS((cur) => (cur ? backend.Settings.createFrom({ ...cur, browser_mode: mode }) : cur))} />
          )}
          {section === "abilities" && (
            <DesktopControlCard
              onChanged={(on) => setS((cur) => (cur ? backend.Settings.createFrom({ ...cur, desktop_control: on }) : cur))}
              onBlockedChanged={(list) => setS((cur) => (cur ? backend.Settings.createFrom({ ...cur, desktop_blocked_apps: list as string[] }) : cur))}
            />
          )}
          {section === "abilities" && (
            <ExternalAgentsCard
              ea={s.external_agents ?? new backend.ExternalAgents()}
              onChange={setEA}
              statuses={agentCLIs}
              probing={probing}
              reached={reachable}
              onRecheck={recheckAgentCLIs}
            />
          )}
          {section === "abilities" && (
            <LocalHiveCard
              onChanged={(fresh) => setS((cur) => (cur ? backend.Settings.createFrom({ ...cur, hive: fresh.hive }) : fresh))}
            />
          )}
          {section === "abilities" && (
            <LinkSuperLeoCard
              onChanged={(fresh) => setS((cur) => (cur ? backend.Settings.createFrom({ ...cur, remote_agents: fresh.remote_agents }) : fresh))}
            />
          )}
          {section === "abilities" && (
            <AddAgentCard
              queen={s.hive?.role === "queen"}
              onQueen={
                s.hive?.role === "worker"
                  ? undefined
                  : (on) =>
                      setS((prev) =>
                        prev
                          ? Object.assign(new backend.Settings(prev), {
                              hive: new backend.HiveSettings({ ...prev.hive, role: on ? "queen" : "", standalone: !on }),
                            })
                          : prev,
                      )
              }
            />
          )}
          {section === "abilities" && (
            <AddWorkerCard
              ra={s.remote_agents ?? new backend.RemoteAgents()}
              onSave={async (agents) => {
                const next = Object.assign(new backend.Settings(s), {
                  remote_agents: Object.assign(new backend.RemoteAgents(s.remote_agents), { agents, enabled: true }),
                });
                await SaveSettings(next);
                setS(next);
                onSaved();
              }}
            />
          )}
          {section === "abilities" && (
            <RemoteAgentsCard
              ra={s.remote_agents ?? new backend.RemoteAgents()}
              onChange={setRA}
              statuses={reachable}
              checking={checking}
              onCheck={checkReachable}
            />
          )}

          <div className="card" hidden={section !== "abilities"}>
            <div className="card-title">{translate("Safety")}</div>
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
                    : `On — unanswered after ${approval?.waitSeconds ?? 120}s counts as no`}
                </span>
              </div>
            </div>
            <AuditTail info={approval} />
          </div>

          {/* ── General ── */}
          <QueenNameCard
            hidden={section !== "general"}
            name={s.queen_name === DEFAULT_QUEEN ? "" : s.queen_name ?? ""}
            aliases={s.queen_aliases ?? []}
            onChange={(name, aliases) => setS((prev) => (prev ? Object.assign(new backend.Settings(prev), { queen_name: name, queen_aliases: aliases }) : prev))}
          />
          {section === "general" && <PairPhoneCard />}
          {!served && section === "general" && <OpenInBrowserCard />}

          <div className="card" hidden={section !== "general"}>
            <div className="card-title">{translate("Notifications")}</div>
            <TextField
              label="Webhook URL"
              value={s.webhook_url}
              onChange={(v) => set("webhook_url", v)}
              placeholder="https://example.com/superleo-hook"
            />
            <PasswordField
              label="Webhook Secret"
              value={s.webhook_secret}
              onChange={(v) => set("webhook_secret", v)}
            />
            <div className="row">
              <button className="btn" onClick={testWebhook} disabled={webhookTesting || !s.webhook_url}>
                {webhookTesting ? "Sending…" : "Send test"}
              </button>
              {webhookResult && <span className="hint">{webhookResult}</span>}
            </div>
          </div>

          <div className="card" hidden={section !== "general"}>
            <div className="card-title">Telegram</div>
            <PasswordField
              label="Bot Token"
              value={s.telegram_bot_token}
              onChange={(v) => set("telegram_bot_token", v)}
            />
            <TextField
              label="Allowed Chat IDs"
              value={(s.telegram_allowed_chats ?? []).join(", ")}
              onChange={(v) =>
                set(
                  "telegram_allowed_chats",
                  v
                    .split(",")
                    .map((x) => Number(x.trim()))
                    .filter((n) => Number.isFinite(n) && n !== 0),
                )
              }
              placeholder="1669479669"
            />
          </div>

          <ProxyCard
            hidden={section !== "general"}
            proxy={s.proxy ?? new backend.ProxySettings()}
            onChange={(p) => set("proxy", p)}
          />

          <div className="card" hidden={section !== "general"}>
            <div className="card-title">{translate("Runtime")}</div>
            <TextField label="Workspace Directory" value={s.workspace_dir} onChange={(v) => set("workspace_dir", v)} placeholder="~/.superleo/workspace" />
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
              </div>
              <div className="field">
                <label>Token budget per turn</label>
                <input
                  className="input"
                  type="number"
                  min={0}
                  value={s.turn_max_tokens || ""}
                  placeholder="No limit"
                  onChange={(e) => set("turn_max_tokens", Math.max(0, Number(e.target.value) || 0))}
                />
              </div>
              <div className="field">
                <label>Token budget per long task</label>
                <input
                  className="input"
                  type="number"
                  min={0}
                  value={s.long_task_max_tokens || ""}
                  placeholder="No limit"
                  onChange={(e) => set("long_task_max_tokens", Math.max(0, Number(e.target.value) || 0))}
                />
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
                    ? "On — email / phone / 身份证 / 手机号 / 银行卡 masked before reaching the model"
                    : "Off — the model sees your data as-is"}
                </span>
              </div>
            </div>
          </div>

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
