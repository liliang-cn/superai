// The first start, on the desktop and in a browser: may SuperLeo look for the
// AI agents on this Mac (desktop only), and what it thinks with. Nothing is
// looked for before the person says yes, and nothing is chosen for them
// (app/first_run.go). It ends with SuperLeo able to answer, or with the person
// saying "later", never in a window that silently cannot reply.

import { useEffect, useRef, useState } from "react";
import QRCode from "qrcode";
import Sky from "../canvas/Sky";
import "../canvas/canvas.css";
import { loadTheme, themeVars } from "../canvas/theme";
import { CheckModel, DetectAgents, FinishOnboarding, GetSettings, PairedDevices, PairPhone, SaveSettings } from "../../wailsjs/go/app/App";
import { backend } from "../../wailsjs/go/models";
import { useI18n } from "../lib/i18n";
import { machine } from "../lib/machine";

const IDLE = [
  { key: "a", busy: 0.2, rgb: [242, 165, 22] as [number, number, number] },
  { key: "b", busy: 0.15, rgb: [255, 120, 90] as [number, number, number] },
  { key: "c", busy: 0.15, rgb: [70, 180, 170] as [number, number, number] },
  { key: "d", busy: 0.15, rgb: [60, 170, 100] as [number, number, number] },
];

type Agent = { name: string; label: string; signedIn?: boolean; canBeDefault: boolean };

// OpenAI-compatible endpoints people most often have a key for.
const PRESETS = [
  { id: "openai", label: "OpenAI", base: "https://api.openai.com/v1", key: true },
  { id: "deepseek", label: "DeepSeek", base: "https://api.deepseek.com/v1", key: true },
  { id: "openrouter", label: "OpenRouter", base: "https://openrouter.ai/api/v1", key: true },
  { id: "ollama", label: "Ollama", base: "http://127.0.0.1:11434/v1", key: false },
  { id: "other", label: "Other", base: "", key: true },
];

type Check = { ok: boolean; models: string[]; error?: string; listed?: boolean };

export default function Onboarding({ onDone, web = false, start = "ask" }: {
  onDone: () => void;
  /** In a browser: the agents are looked for on the machine SuperLeo runs on. */
  web?: boolean;
  start?: "ask" | "model";
}) {
  const { t } = useI18n();
  const theme = loadTheme();
  const [step, setStep] = useState<"looking" | "found" | "model" | "key" | "phone">(start === "model" ? "model" : "looking");
  const [pick, setPick] = useState("");
  // Where the agents are looked for: this Mac, or the machine a browser's
  // SuperLeo runs on.
  const where = web ? t("the machine SuperLeo runs on") : t("this {machine}", { machine: t(machine) });
  const [agents, setAgents] = useState<Agent[]>([]);
  const [looked, setLooked] = useState(false);
  const [busy, setBusy] = useState(false);
  const [err, setErr] = useState("");

  // The model step.
  const [preset, setPreset] = useState(PRESETS[0].id);
  const [base, setBase] = useState(PRESETS[0].base);
  const [key, setKey] = useState("");
  const [model, setModel] = useState("");
  const [check, setCheck] = useState<Check | null>(null);
  const [testing, setTesting] = useState(false);

  const look = async () => {
    setStep("looking");
    let found: Agent[] = [];
    try {
      found = ((await DetectAgents()) ?? []) as Agent[];
    } catch {
      found = [];
    }
    setAgents(found);
    setPick(found.find((a) => a.canBeDefault && a.signedIn)?.name ?? "");
    setLooked(true);
    setStep("found");
  };
  // The agents on the host are looked for at once: they are the first way to think.
  useEffect(() => { if (step === "looking" && !looked) void look(); }, []);
  // The last step: the phone. Skipped when one is paired already.
  const [phone, setPhone] = useState<{ code: string; qr: string; expires: number } | null>(null);
  const [left, setLeft] = useState(0);
  const [joined, setJoined] = useState("");
  const known = useRef<Set<string> | null>(null);
  const done = async () => {
    try {
      const ds = (await PairedDevices()) ?? [];
      if (ds.length > 0) { onDone(); return; }
      known.current = new Set();
    } catch {
      onDone();
      return;
    }
    setBusy(false);
    setStep("phone");
    void newCode();
  };
  const newCode = async () => {
    setErr("");
    try {
      const c = await PairPhone();
      const link = c.link || `superleo://pair?server=${encodeURIComponent(window.location.origin)}&code=${c.code}`;
      const qr = await QRCode.toDataURL(link, { margin: 1, width: 360, errorCorrectionLevel: "M" });
      setPhone({ code: c.code, qr, expires: new Date(c.expires_at as unknown as string).getTime() });
    } catch (e) {
      setErr(String((e as Error)?.message ?? e));
    }
  };
  // The countdown, and the phone that claims the code, seen without a reload.
  useEffect(() => {
    if (step !== "phone" || !phone || joined) return;
    const tick = () => setLeft(Math.max(0, Math.ceil((phone.expires - Date.now()) / 1000)));
    const poll = async () => {
      const ds = (await PairedDevices().catch(() => [])) ?? [];
      const fresh = ds.find((d) => !known.current?.has(d.id));
      if (fresh) setJoined(fresh.name || t("Your phone"));
    };
    tick();
    const a = window.setInterval(tick, 1000);
    const b = window.setInterval(() => void poll(), 2000);
    return () => { window.clearInterval(a); window.clearInterval(b); };
  }, [step, phone, joined]);

  const finish = async (engine: string, useAgents: boolean) => {
    setBusy(true);
    setErr("");
    try {
      await FinishOnboarding(engine, useAgents);
      await done();
    } catch (e) {
      setErr(String((e as Error)?.message ?? e));
      setBusy(false);
    }
  };

  // The agents that can think for SuperLeo, signed-in ones first.
  const thinkers = agents.filter((a) => a.canBeDefault).sort((x, y) => Number(!!y.signedIn) - Number(!!x.signedIn));
  const chosen = thinkers.find((a) => a.name === pick && a.signedIn);
  const state = (a: Agent) => a.signedIn === true ? t("Signed in") : a.signedIn === false ? t("Not signed in") : t("Installed");

  const pickPreset = (id: string) => {
    const p = PRESETS.find((x) => x.id === id) ?? PRESETS[0];
    setPreset(p.id);
    setBase(p.base);
    setCheck(null);
    setModel("");
  };
  const needsKey = PRESETS.find((p) => p.id === preset)?.key ?? true;

  const test = async (): Promise<Check> => {
    setTesting(true);
    setErr("");
    try {
      const r = (await CheckModel(base, key, model)) as Check;
      // A list came back and no model was named: the first one to start from.
      // Only listed, not asked yet; saving asks it.
      if (!model && r.models?.length) {
        setModel(r.models[0]);
        r.listed = true;
      }
      setCheck(r);
      return r;
    } catch (e) {
      const r = { ok: false, models: [], error: String((e as Error)?.message ?? e) };
      setCheck(r);
      return r;
    } finally {
      setTesting(false);
    }
  };

  // Saved only once the model has answered: a first start that ends on a
  // key that does not work is the silent window this exists to prevent.
  const saveKey = async () => {
    setBusy(true);
    setErr("");
    try {
      const r = model ? await test() : { ok: false, models: [], error: t("Pick a model first.") };
      if (!r.ok) {
        setErr(r.error || t("That did not work."));
        setBusy(false);
        return;
      }
      const s = await GetSettings();
      await SaveSettings(new backend.Settings({ ...s, llm_base_url: base.trim(), llm_key: key.trim(), llm_model: model.trim(), cliproxy_enabled: false }));
      await FinishOnboarding("", looked);
      await done();
    } catch (e) {
      setErr(String((e as Error)?.message ?? e));
      setBusy(false);
    }
  };

  // Accounts sign in through the browser from Settings, where their models
  // are then picked: turned on here, finished there.
  const useAccounts = async () => {
    setBusy(true);
    setErr("");
    try {
      const s = await GetSettings();
      await SaveSettings(new backend.Settings({ ...s, cliproxy_enabled: true }));
      await FinishOnboarding("", looked);
      window.location.hash = "#/settings";
      await done();
    } catch (e) {
      setErr(String((e as Error)?.message ?? e));
      setBusy(false);
    }
  };

  useEffect(() => { setCheck(null); }, [base, key]);

  return (
    <div className="cv-root gate2 gate2-over" style={{ ...themeVars(theme), "--wails-draggable": "no-drag" } as React.CSSProperties}>
      <Sky at={new Date()} lanes={IDLE} base={theme.base} mode={theme.mode} />
      <div className="gate2-drag" style={{ "--wails-draggable": "drag" } as React.CSSProperties} />
      <div className={`cv-glass gate2-card${step === "looking" ? "" : " wide"}`}>
        {step === "looking" && <>
          <h1>{t("Looking…")}</h1>
          <p>{t("Checking which AI agents are installed and signed in.")}</p>
        </>}

        {step === "found" && <>
          <h1>{t("How should SuperLeo think?")}</h1>
          {thinkers.length > 0 ? (
            <ul className="gate2-agents pick" role="radiogroup">
              {thinkers.map((a) => {
                const can = a.canBeDefault && a.signedIn === true;
                return (
                  <li key={a.name} role="radio" aria-checked={pick === a.name} aria-disabled={!can}
                    className={`${pick === a.name ? "on" : ""}${can ? "" : " off"}`}
                    onClick={() => can && setPick(a.name)}>
                    <i className={a.signedIn ? "on" : a.signedIn === false ? "off" : ""} />
                    <span>{a.label}</span>
                    <em>{state(a)}</em>
                  </li>
                );
              })}
            </ul>
          ) : (
            <p>{t("No AI agents found on {where}.", { where })}</p>
          )}
          {chosen && (
            <button className="cv-pill ink gate2-go" disabled={busy} onClick={() => finish(chosen.name, true)}>
              {busy ? t("Setting up…") : t("Use {name}", { name: chosen.label })}
            </button>
          )}
          <button className={chosen ? "gate2-alt" : "cv-pill ink gate2-go"} disabled={busy} onClick={() => setStep("model")}>{t("Use my own model")}</button>
          <button className="gate2-alt" disabled={busy} onClick={() => finish("", true)}>{t("Later")}</button>
          {err && <div className="gate2-err">{err}</div>}
        </>}

        {step === "model" && <>
          <h1>{t("Sign in with your AI account")}</h1>
          <p className="gate2-note">Claude · ChatGPT · Antigravity</p>
          <button className="cv-pill ink gate2-go" disabled={busy} onClick={useAccounts}>
            {busy ? t("Setting up…") : t("Sign in")}
          </button>
          <button className="gate2-alt" disabled={busy} onClick={() => setStep("key")}>{t("Use an API key")}</button>
          <button className="gate2-alt" disabled={busy} onClick={look}>{t("Find agents")}</button>
          <button className="gate2-alt" disabled={busy} onClick={() => finish("", looked)}>{t("Later")}</button>
          {err && <div className="gate2-err">{err}</div>}
        </>}

        {step === "key" && <>
          <h1>{t("API key")}</h1>
          <label className="gate2-label">{t("Service")}</label>
          <div className="gate2-presets">
            {PRESETS.map((p) => (
              <button key={p.id} className={preset === p.id ? "on" : ""} onClick={() => pickPreset(p.id)}>{t(p.label)}</button>
            ))}
          </div>
          <label className="gate2-label">{t("Address")}</label>
          <div className="gate2-field">
            <input value={base} onChange={(e) => setBase(e.target.value)} placeholder="https://…/v1" autoComplete="off" spellCheck={false} />
          </div>
          {needsKey && <>
            <label className="gate2-label">{t("API key")}</label>
            <div className="gate2-field">
              <input type="password" value={key} onChange={(e) => setKey(e.target.value)} placeholder="sk-…" autoComplete="off" spellCheck={false} />
            </div>
          </>}
          <label className="gate2-label">{t("Model")}</label>
          <div className="gate2-field">
            <input value={model} onChange={(e) => { setModel(e.target.value); setCheck(null); }} list="gate2-models" placeholder={t("Test to list the models")} autoComplete="off" spellCheck={false} />
            <datalist id="gate2-models">{(check?.models ?? []).map((m) => <option key={m} value={m} />)}</datalist>
            <button type="button" disabled={testing || !base} onClick={() => void test()}>{testing ? t("Testing…") : t("Test")}</button>
          </div>
          {check && (
            <div className={`gate2-check ${check.ok ? "ok" : "bad"}`}>
              {check.ok
                ? (check.listed ? t("{count} models available; {model} picked.", { count: check.models.length, model }) : t("{model} answered. Ready to save.", { model }))
                : check.error}
            </div>
          )}
          <button className="cv-pill ink gate2-go" disabled={busy || !base || !model || (needsKey && !key)} onClick={saveKey}>
            {busy ? t("Setting up…") : t("Save and start")}
          </button>
          <button className="gate2-alt" disabled={busy} onClick={() => setStep("model")}>{t("Back")}</button>
          {err && <div className="gate2-err">{err}</div>}
        </>}

        {step === "phone" && <>
          <h1>{t("Connect your phone")}</h1>
          {joined ? (
            <div className="gate2-check ok">{t("{name} is connected", { name: joined })}</div>
          ) : phone && left > 0 ? (
            <div className="gate2-pair">
              <img src={phone.qr} alt="" width={180} height={180} />
              <div>
                <b>{phone.code.slice(0, 3)} {phone.code.slice(3)}</b>
                <span>{t("Scan with your phone's camera")}</span>
                <span>{t("Expires in {time}", { time: `${Math.floor(left / 60)}:${String(left % 60).padStart(2, "0")}` })}</span>
              </div>
            </div>
          ) : phone ? (
            <button className="gate2-alt" onClick={() => void newCode()}>{t("Show a new code")}</button>
          ) : (
            <p>{t("Looking…")}</p>
          )}
          {joined
            ? <button className="cv-pill ink gate2-go" onClick={onDone}>{t("Done")}</button>
            : <button className="gate2-alt" onClick={onDone}>{t("Later")}</button>}
          {err && <div className="gate2-err">{err}</div>}
        </>}
      </div>
    </div>
  );
}
