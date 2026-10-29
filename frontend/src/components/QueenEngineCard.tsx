import { useEffect, useState } from "react";
import { QueenEngines } from "../../wailsjs/go/app/App";
import { translate } from "../lib/i18n";
import { queenEngineNote } from "../lib/format";
import { EventsOn } from "../../wailsjs/runtime";

type Choice = { name: string; label: string; online: boolean };

/**
 * Who thinks for the queen when a person talks to her: SuperLeo's own agent,
 * or Claude Code here or on a linked machine. Edits the page's copy of the settings, saved with
 * the rest, like the queen's name above it.
 */
export default function QueenEngineCard({ hidden, engine, fallback, publicURL, onChange }: {
  hidden: boolean;
  engine: string;
  fallback: boolean;
  publicURL: string;
  onChange: (engine: string, fallback: boolean, publicURL: string) => void;
}) {
  const [choices, setChoices] = useState<Choice[]>([]);
  // Who took her latest turn, when the first choice could not.
  const [now, setNow] = useState("");

  useEffect(() => {
    if (hidden) return;
    QueenEngines()
      .then((r: any) => {
        setChoices((r?.choices ?? []) as Choice[]);
        setNow(r?.now ?? "");
      })
      .catch(() => setChoices([{ name: "", label: "SuperLeo", online: true }]));
    const off = EventsOn("queen:engine", (p: { engine?: string }) => setNow(p?.engine ?? ""));
    return () => {
      if (typeof off === "function") off();
    };
  }, [hidden]);
  const inUse = queenEngineNote(engine, now);

  const remote = engine.includes(".");
  return (
    <div className="card" hidden={hidden}>
      <div className="card-title">{translate("Queen engine")}</div>
      <div className="field">
        <label>{translate("Engine")}</label>
        <div className="seg" role="radiogroup" aria-label={translate("Engine")} style={{ flexWrap: "wrap", alignSelf: "flex-start" }}>
          {choices.map((c) => (
            <button
              key={c.name || "superleo"}
              type="button"
              role="radio"
              aria-checked={engine === c.name}
              className={`seg-btn${engine === c.name ? " on" : ""}`}
              title={c.online ? undefined : translate("Not reachable now")}
              style={c.online ? undefined : { opacity: 0.55 }}
              onClick={() => onChange(c.name, fallback, publicURL)}
            >
              {c.label}
            </button>
          ))}
        </div>
        {inUse && <span className="hint">{translate("In use: {engine}", { engine: inUse })}</span>}
      </div>
      {engine !== "" && (
        <div className="field">
          <label>{translate("When it cannot answer")}</label>
          <div className="toggle" onClick={() => onChange(engine, !fallback, publicURL)} style={{ cursor: "pointer", marginTop: 2 }}>
            <div className={`switch${fallback ? " on" : ""}`}><div className="knob" /></div>
            <span style={{ fontSize: 13, color: "var(--text-1)" }}>
              {fallback ? translate("SuperLeo answers, and says so") : translate("The turn fails")}
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
            onChange={(e) => onChange(engine, fallback, e.target.value)}
          />
        </div>
      )}
    </div>
  );
}
