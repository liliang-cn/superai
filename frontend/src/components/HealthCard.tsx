import { useCallback, useEffect, useState } from "react";
import { Doctor } from "../../wailsjs/go/main/App";
import { backend } from "../../wailsjs/go/models";
import { HeartPulse, RotateCw, ChevronDown, ChevronRight } from "lucide-react";

/**
 * Is this install set up correctly?
 *
 * agent.Doctor answers that without calling a model and without connecting to
 * anything, which is what makes it safe to run from a card that opens itself.
 * The card shows three numbers and then only what is wrong: a failing check is
 * expanded with its fix text, because "no LLM provider configured" is useless
 * without "add one in Settings" beside it. Warnings fold away — an install
 * with no embedder and no skills is a working install — and passing checks
 * collapse to a count nobody needs to read.
 */
export default function HealthCard() {
  const [rep, setRep] = useState<backend.DoctorReport | null>(null);
  const [busy, setBusy] = useState(false);
  const [err, setErr] = useState("");
  const [open, setOpen] = useState(false);

  const run = useCallback(async () => {
    setBusy(true);
    try {
      setRep(await Doctor());
      setErr("");
    } catch (e) {
      setErr(String(e));
    } finally {
      setBusy(false);
    }
  }, []);

  useEffect(() => {
    run();
  }, [run]);

  const checks = rep?.checks ?? [];
  const fails = checks.filter((c) => c.status === "fail");
  const warns = checks.filter((c) => c.status === "warn");
  const tone = !rep ? "" : rep.healthy ? (warns.length ? "amber" : "lime") : "rose";

  return (
    <div className="cr-panel cr-health">
      <div className="cr-panel-h">
        <HeartPulse size={13} />
        <span className="cr-eyebrow">Health · the install, not the run</span>
        <span className={`cr-tag ${tone}`}>
          {rep ? (rep.healthy ? (warns.length ? "degraded" : "healthy") : "broken") : busy ? "checking…" : "—"}
        </span>
      </div>

      <div className="cr-health-top">
        <div className="cr-health-counts">
          <span className="lime">
            <b>{rep?.ok ?? 0}</b> ok
          </span>
          <span className={warns.length ? "amber" : "cr-dim"}>
            <b>{rep?.warn ?? 0}</b> warn
          </span>
          <span className={fails.length ? "rose" : "cr-dim"}>
            <b>{rep?.fail ?? 0}</b> fail
          </span>
        </div>
        <button className="cr-btn tiny" onClick={run} disabled={busy} title="Run the checks again">
          <RotateCw size={10} />
          {busy ? "Checking…" : "Check"}
        </button>
      </div>

      {rep?.home && <div className="cr-health-home" title={rep.home}>{rep.home}</div>}

      {(err || rep?.error) && <div className="cr-health-check fail"><b>inspection failed</b><span>{err || rep?.error}</span></div>}

      {fails.map((c) => (
        <div key={c.name} className="cr-health-check fail">
          <b>{c.name}</b>
          <span>{c.detail}</span>
          {c.fix && <em>fix: {c.fix}</em>}
        </div>
      ))}

      {checks.length > 0 && (
        <>
          <button className="cr-health-more" onClick={() => setOpen((o) => !o)}>
            {open ? <ChevronDown size={11} /> : <ChevronRight size={11} />}
            {open ? "hide" : "show"} {warns.length} warning{warns.length === 1 ? "" : "s"} and{" "}
            {checks.length - fails.length - warns.length} passing check
            {checks.length - fails.length - warns.length === 1 ? "" : "s"}
          </button>
          {open && (
            <div className="cr-health-list">
              {checks
                .filter((c) => c.status !== "fail")
                .map((c) => (
                  <div key={c.name} className={`cr-health-check ${c.status}`}>
                    <b>{c.name}</b>
                    <span>{c.detail}</span>
                    {c.status === "warn" && c.fix && <em>fix: {c.fix}</em>}
                  </div>
                ))}
            </div>
          )}
        </>
      )}

      {!rep && !busy && !err && <div className="cr-note">no report yet</div>}
    </div>
  );
}
