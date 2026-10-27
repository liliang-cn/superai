import { useEffect, useState } from "react";
import { QueenEngines } from "../../wailsjs/go/app/App";
import { translate } from "../lib/i18n";

type Choice = { name: string; label: string; online: boolean };

/**
 * Who thinks for the queen when a person talks to her: SuperAI's own agent,
 * or Claude Code here or on a linked machine, then the backups tried in order
 * when it cannot answer. Edits the page's copy of the settings, saved with
 * the rest, like the queen's name above it.
 */
export default function QueenEngineCard({ hidden, engine, backups, fallback, publicURL, onChange }: {
  hidden: boolean;
  engine: string;
  backups: string[];
  fallback: boolean;
  publicURL: string;
  onChange: (engine: string, backups: string[], fallback: boolean, publicURL: string) => void;
}) {
  const [choices, setChoices] = useState<Choice[]>([]);

  useEffect(() => {
    if (hidden) return;
    QueenEngines()
      .then((r: any) => setChoices((r?.choices ?? []) as Choice[]))
      .catch(() => setChoices([{ name: "", label: "SuperAI", online: true }]));
  }, [hidden]);

  const chain = engine === "" ? [] : [engine, ...backups.filter((b) => b !== engine)];
  const remote = chain.some((e) => e.includes("."));
  const labelOf = (name: string) => choices.find((c) => c.name === name)?.label ?? name;
  const spare = choices.filter((c) => c.name !== "" && !chain.includes(c.name));
  const pick = (name: string) => onChange(name, name === "" ? [] : chain.filter((e) => e !== name), fallback, publicURL);
  const setBackups = (next: string[]) => onChange(engine, next, fallback, publicURL);
  const rest = chain.slice(1);
  return (
    <div className="card" hidden={hidden}>
      <div className="card-title">{translate("Queen engine")}</div>
      <div className="card-desc">
        {translate("Who answers when you talk to the queen. Calls, schedules and dashboards stay with SuperAI.")}
      </div>
      <div className="field">
        <label>{translate("Engine")}</label>
        <div className="seg" role="radiogroup" aria-label={translate("Engine")} style={{ flexWrap: "wrap", alignSelf: "flex-start" }}>
          {choices.map((c) => (
            <button
              key={c.name || "superai"}
              type="button"
              role="radio"
              aria-checked={engine === c.name}
              className={`seg-btn${engine === c.name ? " on" : ""}`}
              title={c.online ? undefined : translate("Not reachable now")}
              style={c.online ? undefined : { opacity: 0.55 }}
              onClick={() => pick(c.name)}
            >
              {c.label}
            </button>
          ))}
        </div>
      </div>
      {engine !== "" && (
        <div className="field">
          <label>{translate("Then try")}</label>
          {rest.map((name, i) => (
            <div key={name} style={{ display: "flex", alignItems: "center", gap: 6, fontSize: 13, color: "var(--text-1)" }}>
              <span style={{ minWidth: 18, color: "var(--text-3)" }}>{i + 2}.</span>
              <span style={{ flex: 1 }}>{labelOf(name)}</span>
              <button
                type="button"
                className="seg-btn"
                aria-label={translate("Move up")}
                disabled={i === 0}
                onClick={() => setBackups(rest.map((e, j) => (j === i - 1 ? name : j === i ? rest[i - 1] : e)))}
              >
                ↑
              </button>
              <button type="button" className="seg-btn" aria-label={translate("Remove")} onClick={() => setBackups(rest.filter((e) => e !== name))}>
                ×
              </button>
            </div>
          ))}
          {spare.length > 0 && (
            <select
              className="input"
              aria-label={translate("Add a backup")}
              value=""
              onChange={(e) => e.target.value && setBackups([...rest, e.target.value])}
              style={{ alignSelf: "flex-start" }}
            >
              <option value="">{translate("Add a backup")}</option>
              {spare.map((c) => (
                <option key={c.name} value={c.name}>
                  {c.label}
                </option>
              ))}
            </select>
          )}
        </div>
      )}
      {engine !== "" && (
        <div className="field">
          <label>{translate("When none can answer")}</label>
          <div className="toggle" onClick={() => onChange(engine, backups, !fallback, publicURL)} style={{ cursor: "pointer", marginTop: 2 }}>
            <div className={`switch${fallback ? " on" : ""}`}><div className="knob" /></div>
            <span style={{ fontSize: 13, color: "var(--text-1)" }}>
              {fallback ? translate("SuperAI answers, and says so") : translate("The turn fails")}
            </span>
          </div>
        </div>
      )}
      {remote && (
        <div className="field">
          <label>{translate("Public address")}</label>
          <input
            className="input"
            aria-label={translate("Public address")}
            value={publicURL}
            placeholder="https://ai.example.com"
            autoComplete="off"
            onChange={(e) => onChange(engine, backups, fallback, e.target.value)}
          />
          <span className="hint">{translate("Where the other machine reaches this SuperAI's /mcp.")}</span>
        </div>
      )}
    </div>
  );
}
