import React, { useEffect, useMemo, useRef, useState } from "react";
import {
  BellIcon, ChevronDownIcon, ChevronUpIcon, DatabaseIcon, ExternalLinkIcon, GlobeIcon, HexagonIcon, LaptopIcon,
  LogOutIcon, MonitorIcon, NetworkIcon, PauseIcon, PencilIcon, PlayIcon, PlusIcon, RadarIcon,
  SettingsIcon, ShieldCheckIcon, SparklesIcon, SunIcon, Trash2Icon, WrenchIcon,
} from "lucide-react";
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
import CodingToolsCard from "../components/settings/CodingToolsCard";
import ChannelsCard from "../components/settings/ChannelsCard";
import ResidentGroup from "../components/settings/ResidentGroup";
import { ACCENTS } from "../components/ThemePicker";
import {
  Avatar, Block, Btn, Chips, ConfirmBtn, Group, IconBtn, Menu, Note, Num, Row, Secret, Seg, Select, State, Text, Toggle,
} from "../components/settings/kit";
import "../components/settings/settings.css";
import {
  CLIProxyAccounts,
  CLIProxyLogin,
  CLIProxyProviders,
  CLIProxySetAccountEnabled,
  CLIProxySignOut,
  CLIProxyStatus,
  CLIProxySubmitPrompt,
  GetSettings,
  OpenInBrowser,
  CheckRemoteAgents,
  PatchSettings,
  ToolApprovalInfo,
} from "../../wailsjs/go/app/App";
import { BrowserOpenURL, EventsOff, EventsOn } from "../../wailsjs/runtime/runtime";
import { app, backend } from "../../wailsjs/go/models";
import { toast } from "../lib/toasts";
import { DEFAULT_QUEEN, setQueenName } from "../lib/queenName";
import { clientKind } from "../lib/hivelink";
import { useDeskTheme } from "../desk/DeskShell";
import { COLORS, ColorName } from "../canvas/theme";
import { Accent, Theme } from "../lib/types";

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

// ── Saving ──────────────────────────────────────────────────────────────
//
// The page keeps the settings as last loaded or saved, and the copy being
// edited. Save sends the difference between them as a JSON merge patch
// (PatchSettings), so a field this page did not touch, such as one a card
// saved on its own a moment ago, cannot be put back by it.

type Json = Record<string, any>;

const plain = (v: unknown): Json => JSON.parse(JSON.stringify(v ?? {}));
const isObj = (v: unknown): v is Json => !!v && typeof v === "object" && !Array.isArray(v);

/** What turns a into b, as a merge patch: lists replace whole, objects merge
 *  key by key, and a key b no longer has is null. */
export function mergeDiff(a: Json, b: Json): Json {
  const out: Json = {};
  for (const k of new Set([...Object.keys(a), ...Object.keys(b)])) {
    const av = a[k];
    const bv = b[k];
    if (JSON.stringify(av) === JSON.stringify(bv)) continue;
    if (isObj(av) && isObj(bv)) {
      const d = mergeDiff(av, bv);
      if (Object.keys(d).length) out[k] = d;
    } else {
      out[k] = bv === undefined ? null : bv;
    }
  }
  return out;
}

/** Applies a merge patch to a copy of doc. */
function mergeApply(doc: Json, patch: Json): Json {
  const out: Json = { ...doc };
  for (const [k, v] of Object.entries(patch)) {
    if (v === null) delete out[k];
    else if (isObj(v) && isObj(out[k])) out[k] = mergeApply(out[k], v);
    else out[k] = v;
  }
  return out;
}

// Only the desktop window has anywhere to go. Served, this is the tab you are
// already reading it in, so the group is not rendered at all.
const served = Boolean((window as unknown as Record<string, unknown>).superleoServed);

function OpenInBrowserGroup() {
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
    <Group icon={GlobeIcon} title="Open in browser">
      <Row label="Open in browser" sub={err ? <span className="st-note bad">{err}</span> : origin ? <span className="mono">{origin}</span> : undefined}>
        <Btn icon={ExternalLinkIcon} disabled={busy} onClick={() => void open()}>{busy ? "Starting…" : origin ? "Open another tab" : "Open"}</Btn>
      </Row>
    </Group>
  );
}

type SectionID = "general" | "model" | "abilities" | "notify";

const SECTIONS: { id: SectionID; label: string; icon: typeof SettingsIcon }[] = [
  { id: "general", label: "General", icon: SettingsIcon },
  { id: "model", label: "Model", icon: SparklesIcon },
  { id: "abilities", label: "Abilities", icon: WrenchIcon },
  { id: "notify", label: "Notifications", icon: BellIcon },
];

/**
 * The agents on other machines, each with what it said the last time every
 * agent was asked something (the check in the Hive group's header: it asks a
 * real question, because reachable and usable are different things).
 */
function RemoteAgentRows({ ra, statuses, checking, onRemove }: {
  ra: backend.RemoteAgents;
  statuses: app.RemoteAgentStatus[];
  checking: boolean;
  onRemove: (name: string) => Promise<void>;
}) {
  const configured = Object.entries(ra.agents ?? {});
  return (
    <>
      {configured.map(([name, a]) => {
        const st = statuses.find((x) => x.name === name);
        const hosts = (a.hosts ?? []).join(" · ");
        return (
          <Row key={name} lead={<Avatar>@</Avatar>} label={name} dim={!ra.enabled}
            sub={<span className="mono" title={a.about}>{hosts}</span>}
            sub2={st && !st.reachable && st.detail ? <div className="st-note bad">{st.detail}</div> : undefined}>
            {checking && !st ? <State tone="busy">Asking…</State>
              : st ? st.reachable
                ? <State tone="ok">{translate("{s}s to answer", { s: (st.ms / 1000).toFixed(1) })}</State>
                : <State tone="bad">No answer</State>
              : null}
            <ConfirmBtn icon={Trash2Icon} label="Remove" title={translate("Remove @{name}?", { name })} onConfirm={() => onRemove(name)} />
          </Row>
        );
      })}
    </>
  );
}

/** The tail of the approval audit log, behind a button: the evidence for the
 *  switch above it, for whoever wants to look. */
function AuditRows({ info }: { info: ApprovalInfo | null }) {
  const [open, setOpen] = useState(false);
  if (!info) return null;
  const allowed = info.entries.filter((e) => e.allowed).length;
  const denied = info.entries.length - allowed;
  return (
    <>
      <Row label="Recent decisions" sub={translate("{a} allowed · {d} denied", { a: allowed, d: denied })}>
        <Btn trail={open ? ChevronUpIcon : ChevronDownIcon} onClick={() => setOpen((v) => !v)} data-testid="audit-toggle" aria-expanded={open}>
          {open ? "Hide" : "Details"}
        </Btn>
      </Row>
      {open && (
        <Block testId="audit-list">
          {info.entries.length === 0 ? <Note>{translate("None")}</Note> : (
            <div className="st-audit">
              {/* Newest first on screen; the file itself is append-order. */}
              {[...info.entries].reverse().map((e, i) => (
                <div key={`${e.at}-${i}`} className={`st-audit-r${e.allowed ? "" : " denied"}`}>
                  <span>{e.allowed ? "allow" : "deny "}</span>
                  <span>{e.tool}</span>
                  <span className="c" title={e.summary || e.reason || ""}>{e.summary || e.reason || ""}</span>
                  <span className="w">{e.decided_by}</span>
                </div>
              ))}
            </div>
          )}
          <Note><span className="mono">{info.auditPath}</span></Note>
        </Block>
      )}
    </>
  );
}

/** Light or dark, and the finish. The desktop window's look is the desk's
 *  (colour and mode); a browser tab's is the app's theme and accent. */
function AppearanceRows({ theme, onTheme, accent, onAccent }: {
  theme?: Theme;
  onTheme?: (t: Theme) => void;
  accent?: Accent;
  onAccent?: (a: Accent) => void;
}) {
  const desk = useDeskTheme();
  const onDesk = clientKind() === "desktop";
  const mode = onDesk ? desk.theme.mode : theme ?? "light";
  const setMode = (m: "light" | "dark") => (onDesk ? desk.setTheme({ ...desk.theme, mode: m }) : onTheme?.(m));
  const swatches = onDesk
    ? (Object.keys(COLORS) as ColorName[]).map((k) => ({ key: k, label: COLORS[k].label, swatch: `rgb(${COLORS[k].rgb.join(",")})` }))
    : ACCENTS.map((a) => ({ key: a.key as string, label: a.label, swatch: a.swatch }));
  const current = onDesk ? desk.theme.color : accent;
  const pick = (k: string) => (onDesk ? desk.setTheme({ ...desk.theme, color: k as ColorName }) : onAccent?.(k as Accent));
  return (
    <>
      <LanguageSettings />
      <Row label="Theme">
        <Seg label="Theme" value={mode} onChange={setMode} testId="theme"
          options={[{ value: "light", label: "Light" }, { value: "dark", label: "Dark" }]} />
      </Row>
      <Row label="Accent">
        <div className="st-swatches" role="radiogroup" aria-label={translate("Accent")}>
          {swatches.map((s) => (
            <button key={s.key} type="button" role="radio" aria-checked={current === s.key} aria-label={s.label} title={s.label}
              className={`st-swatch${current === s.key ? " on" : ""}`} style={{ ["--sw" as string]: s.swatch }} onClick={() => pick(s.key)} />
          ))}
        </div>
      </Row>
    </>
  );
}

export default function SettingsView({ onSaved, theme, onTheme, accent, onAccent }: {
  onSaved: () => void;
  theme?: Theme;
  onTheme?: (t: Theme) => void;
  accent?: Accent;
  onAccent?: (a: Accent) => void;
}) {
  const { t } = useI18n();
  // As last loaded or saved, and as being edited. Save sends the difference.
  const [orig, setOrig] = useState<Json | null>(null);
  const [s, setS] = useState<backend.Settings | null>(null);
  const [saving, setSaving] = useState(false);
  // Which section is on screen. Not in the URL and not persisted: the page
  // always opens on General.
  const [section, setSection] = useState<SectionID>("general");
  const [narrow, setNarrow] = useState(false);
  const pageRef = useRef<HTMLDivElement | null>(null);
  const [proxy, setProxy] = useState<ProxyStatus | null>(null);
  const [providers, setProviders] = useState<backend.CLIProxyProvider[]>([]);
  const [projectFor, setProjectFor] = useState<backend.CLIProxyProvider | null>(null);
  const [projectID, setProjectID] = useState("");
  const [login, setLogin] = useState<{ status: string; provider: string; message: string } | null>(null);
  const [pasted, setPasted] = useState("");
  const [editEmbed, setEditEmbed] = useState(false);
  const [adding, setAdding] = useState<"" | "machine" | "link" | "worker">("");

  const [accounts, setAccounts] = useState<backend.CLIProxyAccount[]>([]);
  const [approval, setApproval] = useState<ApprovalInfo | null>(null);
  // What each agent answered when last asked; asking takes seconds and a token.
  const [reachable, setReachable] = useState<app.RemoteAgentStatus[]>([]);
  const [checking, setChecking] = useState(false);

  // The nav goes on top when the page is too narrow for it beside the groups,
  // whatever the page sits in: a window, the desk's panel, a phone.
  useEffect(() => {
    const el = pageRef.current;
    if (!el) return;
    const ro = new ResizeObserver(() => setNarrow(el.clientWidth < 720));
    ro.observe(el);
    return () => ro.disconnect();
  }, [s === null]);

  const patch = useMemo(() => (orig && s ? mergeDiff(orig, plain(s)) : {}), [orig, s]);
  const dirty = Object.keys(patch).length > 0;

  // The latest of both, for callbacks that land after an await.
  const origRef = useRef<Json | null>(null);
  const sRef = useRef<backend.Settings | null>(null);
  origRef.current = orig;
  sRef.current = s;

  /** Takes settings saved elsewhere (a card, a direct patch) as the new
   *  baseline, keeping whatever is still unsaved on the page on top. */
  const absorb = (fresh: backend.Settings | Json) => {
    const base = plain(fresh);
    const o = origRef.current;
    const cur = sRef.current;
    const pending = o && cur ? mergeDiff(o, plain(cur)) : {};
    const next = backend.Settings.createFrom(mergeApply(base, pending));
    origRef.current = base;
    sRef.current = next;
    setOrig(base);
    setS(next);
  };
  /** The same, for a few fields a card has just saved itself. */
  const absorbFields = (p: Json) => absorb(mergeApply(origRef.current ?? {}, p));

  /** Saves just these fields now, outside the page's Save. */
  const patchNow = async (p: Json) => {
    const fresh = await PatchSettings(p);
    absorb(fresh);
    return fresh;
  };

  /** Saves what is unsaved on the page, if anything: probes and tests run
   *  against the settings in force, not the ones on screen. */
  const flush = async () => {
    if (!dirty) return;
    const fresh = await PatchSettings(patch);
    setOrig(plain(fresh));
    setS(fresh);
  };

  /** Save, then actually ask every agent something. */
  async function checkReachable() {
    setChecking(true);
    setReachable([]);
    try {
      await flush();
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
      .then((res) => {
        setOrig(plain(res));
        setS(res);
      })
      .catch((e) => toast.error(String(e?.message || e)));
    CLIProxyProviders().then(setProviders).catch(() => setProviders([]));
    refreshProxy();
    refreshApproval();

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
  const setNested = <K extends "remote_agents" | "external_agents" | "hive">(k: K, p: Json) => {
    setS((prev) => (prev ? backend.Settings.createFrom({ ...plain(prev), [k]: { ...plain(prev[k]), ...p } }) : prev));
  };

  const save = async () => {
    if (!s || !dirty) return;
    setSaving(true);
    try {
      const fresh = await PatchSettings(patch);
      setOrig(plain(fresh));
      setS(fresh);
      setQueenName(fresh.queen_name, fresh.queen_aliases);
      toast.success(translate("Saved"));
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

  const signIn = (prov: backend.CLIProxyProvider, project = "") => {
    setProjectFor(null);
    setLogin({ status: "started", provider: prov.id, message: translate("Starting…") });
    CLIProxyLogin(prov.id, project).then(() => refreshProxy()).catch((e) => setLogin({ status: "error", provider: prov.id, message: String(e?.message || e) }));
  };
  const providerLabel = (id: string) => providers.find((p) => p.id === id)?.label || id;

  if (!s) {
    return (
      <div className="view">
        <div className="loading-row"><span className="spinner" style={{ borderTopColor: "var(--accent)" }} /> {t("Loading settings…")}</div>
      </div>
    );
  }

  const ra = s.remote_agents ?? new backend.RemoteAgents();
  const loginBusy = login?.status === "started" || login?.status === "prompt";
  const embedHost = (() => {
    try {
      return s.embed_base_url ? new URL(s.embed_base_url).host : "";
    } catch {
      return s.embed_base_url;
    }
  })();

  return (
    <div className={`view st-page${narrow ? " narrow" : ""}`} ref={pageRef} data-testid="settings">
      <nav className="st-nav" aria-label={t("Settings")} role="tablist">
        <h2 className="st-nav-h">{t("Settings")}</h2>
        {SECTIONS.map((x) => (
          <button key={x.id} type="button" role="tab" aria-selected={section === x.id} data-testid={`st-nav-${x.id}`}
            className={`st-nav-i${section === x.id ? " on" : ""}`} onClick={() => setSection(x.id)}>
            <x.icon size={16} strokeWidth={1.8} aria-hidden />
            {t(x.label)}
          </button>
        ))}
      </nav>

      <main className="st-main">
        <div className="st">
          {/* ── General ── */}
          {section === "general" && (
            <>
              <Group icon={SunIcon} title="Appearance">
                <AppearanceRows theme={theme} onTheme={onTheme} accent={accent} onAccent={onAccent} />
              </Group>

              <Group icon={HexagonIcon} title="Queen">
                <Row label="Name">
                  <Text label="Name" placeholder={DEFAULT_QUEEN} testId="queen-name"
                    value={s.queen_name === DEFAULT_QUEEN ? "" : s.queen_name ?? ""} onChange={(v) => set("queen_name", v)} />
                </Row>
                <Row label="Also answers to">
                  <Chips testId="queen-aliases" items={s.queen_aliases ?? []} removeLabel="Remove" addLabel="Add an alias" placeholder="bee"
                    onRemove={(a) => set("queen_aliases", (s.queen_aliases ?? []).filter((x) => x !== a))}
                    onAdd={(a) => {
                      const next = a.split(/[,，\s]+/).filter(Boolean).filter((x) => !(s.queen_aliases ?? []).includes(x));
                      if (next.length) set("queen_aliases", [...(s.queen_aliases ?? []), ...next]);
                    }} />
                </Row>
              </Group>

              <Group icon={MonitorIcon} title="Runtime">
                <Row label="Workspace directory">
                  <Text mono label="Workspace directory" placeholder="~/.superleo/workspace" value={s.workspace_dir} onChange={(v) => set("workspace_dir", v)} />
                </Row>
                <ProxyCard proxy={s.proxy ?? new backend.ProxySettings()} saved={orig?.proxy} onChange={(p) => set("proxy", p)} />
                <Row label="Rounds per task">
                  <Num label="Rounds per task" min={1} value={s.max_rounds} onChange={(v) => set("max_rounds", v)} testId="max-rounds" />
                </Row>
                <Row label="Token budget per turn">
                  <Num label="Token budget per turn" placeholder="No limit" value={s.turn_max_tokens} onChange={(v) => set("turn_max_tokens", v)} />
                </Row>
                <Row label="Token budget per long task">
                  <Num label="Token budget per long task" placeholder="No limit" value={s.long_task_max_tokens} onChange={(v) => set("long_task_max_tokens", v)} />
                </Row>
                <Row label="PII redaction">
                  <Toggle label="PII redaction" on={!!s.pii_redaction} onChange={(v) => set("pii_redaction", v)} testId="pii" />
                </Row>
                <Row label="PTC tool calling">
                  <Toggle label="PTC tool calling" on={!s.disable_ptc} onChange={(v) => set("disable_ptc", !v)} />
                </Row>
              </Group>

              {!served && <OpenInBrowserGroup />}
              {!served && <ResidentGroup />}
            </>
          )}

          {/* ── Model: what it thinks and remembers with ── */}
          {section === "model" && (
            <>
              <Group icon={SparklesIcon} title="Model" testId="model-group"
                action={s.cliproxy_enabled && providers.length > 0 ? (
                  <Menu disabled={loginBusy}
                    trigger={<Btn icon={PlusIcon} trail={ChevronDownIcon} disabled={loginBusy} data-testid="add-account">Add account</Btn>}
                    items={providers.map((p) => ({
                      key: p.id,
                      hint: p.note,
                      lead: <Avatar>{p.label.slice(0, 1).toUpperCase()}</Avatar>,
                      label: p.label,
                      onSelect: () => (p.needs_project ? setProjectFor(p) : signIn(p)),
                    }))} />
                ) : undefined}>
                <Row label="Source">
                  <Seg label="Source" testId="model-source" value={s.cliproxy_enabled ? "accounts" : "api"}
                    options={[{ value: "accounts", label: "My accounts" }, { value: "api", label: "Custom API" }]}
                    onChange={(v) => set("cliproxy_enabled", v === "accounts")} />
                </Row>

                {s.cliproxy_enabled ? (
                  <>
                    {accounts.map((acc) => {
                      const label = providerLabel(acc.provider);
                      return (
                        <Row key={acc.file} dim={acc.disabled} testId="account"
                          lead={<Avatar tone={acc.disabled ? "muted" : undefined}>{label.slice(0, 1).toUpperCase()}</Avatar>}
                          label={label} sub={`${acc.account}${acc.project ? ` · ${acc.project}` : ""}`}>
                          {acc.disabled ? <State tone="off">Paused</State> : <State tone="ok">Available</State>}
                          <IconBtn icon={acc.disabled ? PlayIcon : PauseIcon} label={acc.disabled ? "Resume" : "Pause"}
                            onClick={() => void CLIProxySetAccountEnabled(acc.file, acc.disabled).then(() => setTimeout(refreshProxy, 800))} />
                          <ConfirmBtn icon={LogOutIcon} label="Sign out" testId="sign-out"
                            title={translate("Sign out of {name}?", { name: label })} sub={acc.account}
                            onConfirm={() => CLIProxySignOut(acc.file).then(() => { setTimeout(refreshProxy, 800); })} />
                        </Row>
                      );
                    })}
                    {projectFor && (
                      <Row label={projectFor.label} sub={projectFor.note}>
                        <Text mono label="Google Cloud project ID" placeholder="Google Cloud project ID" value={projectID} onChange={setProjectID}
                          onEnter={() => projectID.trim() && signIn(projectFor, projectID.trim())} />
                        <Btn pri disabled={!projectID.trim()} onClick={() => signIn(projectFor, projectID.trim())}>Sign in</Btn>
                      </Row>
                    )}
                    {login && (
                      <Row label={providerLabel(login.provider)}
                        sub={login.status === "error" ? <span className="st-note bad">{login.message}</span> : login.message}>
                        {login.status === "prompt" ? (
                          <>
                            <Text mono label="Callback URL" placeholder="Paste the callback URL from your browser" value={pasted} onChange={setPasted}
                              onEnter={() => { void CLIProxySubmitPrompt(pasted); setLogin({ ...login, status: "started", message: translate("Finishing sign-in…") }); }} />
                            <Btn pri disabled={!pasted.trim()} onClick={() => {
                              void CLIProxySubmitPrompt(pasted);
                              setLogin({ ...login, status: "started", message: translate("Finishing sign-in…") });
                            }}>Submit</Btn>
                          </>
                        ) : login.status === "started" ? <State tone="busy">Signing in…</State>
                          : login.status === "done" ? <State tone="ok">Signed in</State>
                          : <State tone="bad">Failed</State>}
                      </Row>
                    )}
                    {accounts.length === 0 && !login && !projectFor && <Row label="No accounts yet" dim />}
                    <Row label="Model" sub={proxy && !proxy.running && proxy.error ? <span className="st-note bad">{proxy.error}</span> : undefined}>
                      <Select mono label="Model" testId="account-model" value={s.llm_model} placeholder="Choose a model"
                        options={(proxy?.models ?? []).map((m) => ({ value: m, label: m }))} onChange={(v) => set("llm_model", v)} />
                    </Row>
                  </>
                ) : (
                  <>
                    <Row label="Base URL">
                      <Text mono label="Base URL" placeholder="https://api.openai.com/v1" value={s.llm_base_url} onChange={(v) => set("llm_base_url", v)} />
                    </Row>
                    <Row label="API key">
                      <Secret label="API key" value={s.llm_key} onChange={(v) => set("llm_key", v)} />
                    </Row>
                    <Row label="Model">
                      <Text mono label="Model" placeholder="gpt-5.5" value={s.llm_model} onChange={(v) => set("llm_model", v)} />
                    </Row>
                  </>
                )}
              </Group>

              {/* The queen's engine: its own card, owned and styled apart. */}
              {s.hive?.role !== "worker" && (
                <QueenEngineCard
                  hidden={false}
                  engine={s.hive?.queen_engine ?? ""}
                  fallback={s.hive?.queen_fallback !== false}
                  publicURL={s.hive?.public_url ?? ""}
                  onChange={(engine, fallback, publicURL) =>
                    setNested("hive", { queen_engine: engine, queen_fallback: fallback, public_url: publicURL })
                  }
                />
              )}

              <Group icon={DatabaseIcon} title="Memory">
                <Row label="Stored in">
                  <Seg label="Stored in" testId="memory-backend" value={s.memory_backend === "shared" ? "shared" : "local"}
                    options={[{ value: "local", label: "This machine" }, { value: "shared", label: "Shared CortexDB" }]}
                    onChange={(v) => set("memory_backend", v)} />
                </Row>
                {s.memory_backend === "shared" && (
                  <>
                    <Row label="Address">
                      <Text mono label="Address" placeholder="192.168.1.10:47821" value={s.shared_memory_endpoint} onChange={(v) => set("shared_memory_endpoint", v)} />
                    </Row>
                    <Row label="Token">
                      <Secret label="Token" value={s.shared_memory_token} onChange={(v) => set("shared_memory_token", v)} />
                    </Row>
                    <Row label="Namespace">
                      <Text mono label="Namespace" placeholder="default" value={s.shared_memory_namespace} onChange={(v) => set("shared_memory_namespace", v)} />
                    </Row>
                  </>
                )}
                <Row label="Embeddings" sub={[embedHost, s.embed_model].filter(Boolean).join(" · ") || translate("Not set")}>
                  <IconBtn icon={PencilIcon} label={editEmbed ? "Done" : "Edit"} pressed={editEmbed} onClick={() => setEditEmbed((v) => !v)} data-testid="edit-embed" />
                </Row>
                {editEmbed && (
                  <>
                    <Row label="Base URL">
                      <Text mono label="Base URL" placeholder="https://api.openai.com/v1" value={s.embed_base_url} onChange={(v) => set("embed_base_url", v)} />
                    </Row>
                    <Row label="API key">
                      <Secret label="API key" value={s.embed_key} onChange={(v) => set("embed_key", v)} />
                    </Row>
                    <Row label="Model">
                      <Text mono label="Model" placeholder="text-embedding-3-small" value={s.embed_model} onChange={(v) => set("embed_model", v)} />
                    </Row>
                  </>
                )}
              </Group>
            </>
          )}

          {/* ── Abilities: what it can reach, and who else it can hand work to ── */}
          {section === "abilities" && (
            <>
              <Group icon={LaptopIcon} title="Computer">
                {/* Both apply at once, so the page's own copy is told too: a
                    later Save must not put the old value back. */}
                <BrowserModeCard onChanged={(mode) => absorbFields({ browser_mode: mode })} />
                <DesktopControlCard
                  onChanged={(on) => absorbFields({ desktop_control: on })}
                  onBlockedChanged={(list) => absorbFields({ desktop_blocked_apps: list })}
                />
              </Group>

              <CodingToolsCard />

              <Group icon={NetworkIcon} title="Hive" testId="hive-group"
                action={
                  <>
                    <IconBtn icon={RadarIcon} label="Ask every agent" spin={false} disabled={checking} onClick={() => void checkReachable()} data-testid="ask-all" />
                    <Menu
                      trigger={<Btn icon={PlusIcon} trail={ChevronDownIcon} data-testid="hive-add">Add</Btn>}
                      items={[
                        { key: "machine", label: translate("A machine"), onSelect: () => setAdding("machine") },
                        { key: "link", label: translate("Another SuperLeo"), onSelect: () => setAdding("link") },
                        { key: "worker", label: translate("A worker by its command"), onSelect: () => setAdding("worker") },
                      ]} />
                  </>
                }>
                <AddAgentCard
                  queen={s.hive?.role === "queen"}
                  onQueen={s.hive?.role === "worker" ? undefined : (on) => setNested("hive", { role: on ? "queen" : "", standalone: !on })}
                  open={adding === "machine"}
                  onClose={() => setAdding("")}
                />
                <LocalHiveCard onChanged={(fresh) => absorbFields({ hive: plain(fresh.hive) })} />
                <Row label="Agents on other machines">
                  <Toggle label="Agents on other machines" on={!!ra.enabled} onChange={(v) => setNested("remote_agents", { enabled: v })} />
                </Row>
                <LinkSuperLeoCard open={adding === "link"} onClose={() => setAdding("")}
                  onChanged={(fresh) => absorbFields({ remote_agents: plain(fresh.remote_agents) })} />
                <RemoteAgentRows ra={ra} statuses={reachable} checking={checking}
                  onRemove={async (name) => {
                    try {
                      await patchNow({ remote_agents: { agents: { [name]: null } } });
                    } catch (e: any) {
                      toast.error(String(e?.message || e));
                    }
                  }} />
                <AddWorkerCard ra={ra} open={adding === "worker"} onClose={() => setAdding("")}
                  onSave={async (agents) => {
                    const before = Object.keys(plain(origRef.current?.remote_agents?.agents));
                    const p: Json = plain(agents);
                    for (const k of before) if (!(k in p)) p[k] = null;
                    await patchNow({ remote_agents: { agents: p, enabled: true } });
                    onSaved();
                  }} />
              </Group>

              <Group icon={ShieldCheckIcon} title="Ask before running">
                {/* On = asking, the inverse of the stored disable flag, so a
                    file that never heard of the setting still comes up gated. */}
                <Row label="Ask before commands, installs and deletions">
                  {!s.disable_tool_approval && (
                    <span className="st-lab">{translate("{n}s unanswered counts as no", { n: approval?.waitSeconds ?? 120 })}</span>
                  )}
                  <Toggle label="Ask before running" on={!s.disable_tool_approval} onChange={(v) => set("disable_tool_approval", !v)} testId="ask-before" />
                </Row>
                <AuditRows info={approval} />
              </Group>
            </>
          )}

          {/* ── Notifications: where SuperLeo reaches you ── */}
          {section === "notify" && (
            <>
              <PairPhoneCard />

              <ChannelsCard />
            </>
          )}
        </div>

        <div className="st-save" data-testid="save-bar">
          {dirty && <Note>{translate("Unsaved changes")}</Note>}
          {dirty && <Btn onClick={() => orig && setS(backend.Settings.createFrom(orig))} disabled={saving}>Discard</Btn>}
          <Btn pri disabled={!dirty || saving} onClick={() => void save()} data-testid="save">{saving ? "Saving…" : "Save"}</Btn>
        </div>
      </main>
    </div>
  );
}
