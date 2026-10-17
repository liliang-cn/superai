import type React from "react";
import { fmtK, tokPerSec, usePulse } from "../components/Reactor";
import { isToday, type Hive } from "../canvas/data";
import type { CodingRun } from "../canvas/tiles";
import { useTween } from "../lib/useTween";
import { useI18n } from "../lib/i18n";

/** A number that eases to its next value instead of jumping. */
function N({ v, fmt = fmtK }: { v: number; fmt?: (n: number) => string }) {
  return <>{fmt(useTween(v))}</>;
}

/** One figure: its number, then its name. main is what a narrow screen keeps. */
function Fig({ v, label, big, main, bad }: { v: React.ReactNode; label: string; big?: boolean; main?: boolean; bad?: boolean }) {
  return (
    <div className={`wh-fig${big ? " big" : ""}${main ? " main" : ""}${bad ? " bad" : ""}`}>
      <b>{v}</b><span>{label}</span>
    </div>
  );
}

const intK = (n: number) => fmtK(Math.round(n));
const int = (n: number) => String(Math.round(n));

/** The top-left pane: what the core has spent. It stands in the corner's
 *  column, under the hive's pill, so the two never cover each other. */
export function HoloTokens({ on }: { on: boolean }) {
  const { t } = useI18n();
  const p = usePulse();
  return (
    <div className={`wl-holo tl${on ? "" : " off"}`} aria-hidden={!on}>
      <Fig big main v={<><i className={p.live ? "wh-dot on" : "wh-dot"} /><N v={p.tokens} /></>} label={t("tokens this process")} />
      <div className="wh-row">
        <Fig v={<N v={tokPerSec(p)} fmt={(n) => (n >= 10 ? intK(n) : n.toFixed(1))} />} label="tok/s" />
        <Fig v={<N v={p.cached} />} label={t("cached")} />
      </div>
    </div>
  );
}

/**
 * The core's live figures as panes of light pinned to the corners of the 3D
 * view, over the scene and out of the pointer's way: what it has done (top
 * right), what the hive is carrying (bottom left), what it weighs (bottom
 * right); the top left is HoloTokens. Pushed by the pulse, like everything
 * else that moves here.
 */
export default function StatsHud({ hive, runs, on }: { hive: Hive; runs: CodingRun[]; on: boolean }) {
  const { t } = useI18n();
  const p = usePulse();
  const cls = (corner: string) => `wl-holo ${corner}${on ? "" : " off"}`;
  const running = hive.tasks.filter((x) => x.state === "running");
  const busy = new Set(running.map((x) => x.worker)).size;
  const coding = runs.filter((r) => r.state === "running").length;
  const failed = hive.tasks.filter((x) => x.state === "failed" && isToday(x.started_at)).length;
  return (
    <>
      <div className={cls("tr")} aria-hidden={!on}>
        <div className="wh-row">
          <Fig main v={<N v={p.rounds} fmt={int} />} label={t("turns")} />
          <Fig v={<N v={p.calls} fmt={intK} />} label={t("tool calls")} />
        </div>
        <div className="wh-line">
          <span>{t("shells")} <b>{p.shells}</b></span><span>{t("writes")} <b>{p.writes}</b></span>
          <span>mcp <b>{p.mcp}</b></span><span>{t("memory")} <b>{p.memory}</b></span>
        </div>
      </div>
      <div className={cls("bl")} aria-hidden={!on}>
        <div className="wh-row">
          <Fig main v={running.length} label={t("orders in flight")} />
          <Fig v={coding} label={t("coding runs going")} />
        </div>
        <div className="wh-row">
          <Fig v={busy} label={t("workers busy")} />
          <Fig v={failed} label={t("failed today")} bad={failed > 0} />
        </div>
      </div>
      <div className={cls("br")} aria-hidden={!on}>
        <div className="wh-row">
          <Fig main v={<N v={p.cpu} fmt={(n) => `${Math.round(n)}%`} />} label="cpu" />
          <Fig v={<N v={p.heap / 1048576} fmt={(n) => `${Math.round(n)} MB`} />} label={t("heap")} />
        </div>
      </div>
    </>
  );
}
