// The desktop app's first start: may SuperAI look for the AI agents on this
// Mac, and should it think with one of them. Nothing is looked for before the
// person says yes, and nothing is chosen for them (app/first_run.go).

import { useState } from "react";
import Sky from "../canvas/Sky";
import "../canvas/canvas.css";
import { loadTheme, themeVars } from "../canvas/theme";
import { DetectAgents, FinishOnboarding } from "../../wailsjs/go/app/App";
import { useI18n } from "../lib/i18n";
import { machine } from "../lib/machine";

const IDLE = [
  { key: "a", busy: 0.2, rgb: [242, 165, 22] as [number, number, number] },
  { key: "b", busy: 0.15, rgb: [255, 120, 90] as [number, number, number] },
  { key: "c", busy: 0.15, rgb: [70, 180, 170] as [number, number, number] },
  { key: "d", busy: 0.15, rgb: [60, 170, 100] as [number, number, number] },
];

// How many found agents are listed one per line.
const SHOWN = 4;

type Agent = { name: string; label: string; signedIn?: boolean; canBeDefault: boolean };

export default function Onboarding({ onDone }: { onDone: () => void }) {
  const { t } = useI18n();
  const theme = loadTheme();
  const [step, setStep] = useState<"ask" | "looking" | "found">("ask");
  const [agents, setAgents] = useState<Agent[]>([]);
  const [busy, setBusy] = useState(false);
  const [err, setErr] = useState("");

  const look = async () => {
    setStep("looking");
    try {
      setAgents(((await DetectAgents()) ?? []) as Agent[]);
    } catch {
      setAgents([]);
    }
    setStep("found");
  };
  const finish = async (engine: string, useAgents: boolean) => {
    setBusy(true);
    setErr("");
    try {
      await FinishOnboarding(engine, useAgents);
      onDone();
    } catch (e) {
      setErr(String((e as Error)?.message ?? e));
      setBusy(false);
    }
  };

  // The one SuperAI can think with, when it is signed in.
  const best = agents.find((a) => a.canBeDefault && a.signedIn);
  const unsigned = agents.find((a) => a.canBeDefault && a.signedIn === false);
  // The signed-in ones first; a long tail of installed ones is one line.
  const ranked = [...agents].sort((x, y) => Number(!!y.canBeDefault) - Number(!!x.canBeDefault) || Number(!!y.signedIn) - Number(!!x.signedIn));
  const shown = ranked.slice(0, SHOWN);
  const rest = ranked.slice(SHOWN);
  const state = (a: Agent) => a.signedIn === true ? t("Signed in") : a.signedIn === false ? t("Not signed in") : t("Installed");

  return (
    <div className="cv-root gate2" style={{ ...themeVars(theme), "--wails-draggable": "no-drag" } as React.CSSProperties}>
      <Sky at={new Date()} lanes={IDLE} base={theme.base} mode={theme.mode} />
      <div className="gate2-drag" style={{ "--wails-draggable": "drag" } as React.CSSProperties} />
      <div className="cv-glass gate2-card">
        {step === "ask" && <>
          <h1>{t("Find your AI agents")}</h1>
          <p>{t("SuperAI can work with Claude Code, Codex and the other AI agents you already use. May it look for them on this {machine}? It only checks what is installed here.", { machine: t(machine) })}</p>
          <button className="cv-pill ink gate2-go" onClick={look}>{t("Look for agents")}</button>
          <button className="gate2-alt" disabled={busy} onClick={() => finish("", false)}>{t("Not now")}</button>
        </>}

        {step === "looking" && <>
          <h1>{t("Looking…")}</h1>
          <p>{t("Checking which AI agents are installed and signed in.")}</p>
        </>}

        {step === "found" && <>
          {agents.length > 0 && (
            <ul className="gate2-agents">
              {shown.map((a) => (
                <li key={a.name}>
                  <i className={a.signedIn ? "on" : a.signedIn === false ? "off" : ""} />
                  <span>{a.label}</span>
                  <em>{state(a)}</em>
                </li>
              ))}
              {rest.length > 0 && (
                <li className="more" title={rest.map((a) => a.label).join(", ")}>
                  {t("+{count} more: {names}", { count: rest.length, names: rest.map((a) => a.label).join(", ") })}
                </li>
              )}
            </ul>
          )}
          {best ? <>
            <h1>{t("Use {name}?", { name: best.label })}</h1>
            <p>{t("SuperAI will think with {name}, on your own subscription: no key to paste, no model to pick. You can change this in Settings.", { name: best.label })}</p>
            <button className="cv-pill ink gate2-go" disabled={busy} onClick={() => finish(best.name, true)}>
              {busy ? t("Setting up…") : t("Use {name}", { name: best.label })}
            </button>
            <button className="gate2-alt" disabled={busy} onClick={() => finish("", true)}>{t("Not now")}</button>
          </> : <>
            <h1>{agents.length > 0 ? t("Almost there") : t("No AI agents found")}</h1>
            <p>{unsigned
              ? t("Sign in to {name} and SuperAI can think with it. Or choose a model in Settings.", { name: unsigned.label })
              : t("To start, choose a model in Settings: sign in with your Claude, Codex or Gemini account, or add an API key.")}</p>
            <button className="cv-pill ink gate2-go" disabled={busy} onClick={() => finish("", agents.length > 0)}>{t("Continue")}</button>
          </>}
          {err && <div className="gate2-err">{err}</div>}
        </>}
      </div>
    </div>
  );
}
