import { lazy, Suspense, useCallback, useEffect, useRef, useState } from "react";
import { HiveStatus } from "../../wailsjs/go/app/App";
import { EventsOn } from "../../wailsjs/runtime";
import { withDelegates, withRunTasks, type StageHandle, type StagePulse, type StageTask, type StageWorker } from "./hiveFx";
import { useHiveMeter } from "./HiveMeter";
import { useTween } from "../lib/useTween";
import { useCodingRuns } from "../canvas/data";

// The hive in 3D, drawn inside the reactor's disc: the same stage as the Hive
// page (its look, as picked there), with the hive's meter under it. Loaded
// only here, so the Stats page pays for three.js once.
const HiveStage3D = lazy(() => import("./HiveStage3D"));
const HiveHex3D = lazy(() => import("./HiveHex3D"));

type Status = { role: "" | "queen" | "worker"; name: string; members?: StageWorker[]; tasks?: StageTask[] };

function look(): "orbit" | "mycelium" | "tree" | "hex" {
  try {
    const v = JSON.parse(localStorage.getItem("superai-hive-stage") || "null");
    return v?.look === "mycelium" || v?.look === "tree" || v?.look === "hex" ? v.look : "orbit";
  } catch {
    return "orbit";
  }
}

const fmtK = (n: number) => (n >= 1e6 ? (n / 1e6).toFixed(2) + "M" : n >= 1e3 ? (n / 1e3).toFixed(1) + "k" : String(Math.round(n)));

export default function HiveDisc() {
  const [st, setSt] = useState<Status | null>(null);
  const [tasks, setTasks] = useState<StageTask[]>([]);
  const [ready, setReady] = useState(false);
  const stage = useRef<StageHandle>(null);
  const { members, total } = useHiveMeter();
  const runs = useCodingRuns();
  const tokens = useTween(total.tokens);

  // The roster is polled (a worker going quiet sends nothing); tasks and the
  // workers' activity are pushed — the same split as the Hive page.
  const load = useCallback(async () => {
    try {
      const next = (await HiveStatus()) as unknown as Status;
      setSt(next);
      setTasks((cur) => (cur.length === 0 ? next.tasks ?? [] : cur));
      setReady(true);
    } catch {
      /* the next poll tries again */
    }
  }, []);
  useEffect(() => {
    load();
    const t = window.setInterval(load, 3000);
    const offTask = EventsOn("hive:task", (k: StageTask) => {
      if (!k?.id) return;
      setTasks((cur) => {
        const i = cur.findIndex((x) => x.id === k.id);
        if (i < 0) return [...cur, k].slice(-80);
        const next = cur.slice();
        next[i] = k;
        return next;
      });
    });
    const offPulse = EventsOn("hive:pulse", (p: StagePulse) => stage.current?.pulse(p));
    return () => {
      window.clearInterval(t);
      if (typeof offTask === "function") offTask();
      if (typeof offPulse === "function") offPulse();
    };
  }, [load]);

  if (!st || st.role === "") return null;
  const shown = withRunTasks(tasks, runs);
  const workers = withDelegates(st.members ?? [], shown, runs);
  // Working: hive members whose meter says so, and agents outside the hive
  // with an order or a coding run in hand.
  const working = new Set([
    ...members.filter((m) => m.live && m.name !== "queen").map((m) => m.name),
    ...shown.filter((t) => t.state === "running").map((t) => t.worker),
  ]).size;
  const l = look();
  const props = { role: st.role, self: st.name, workers, tasks: shown, ready };
  return (
    <div className="hd">
      <Suspense fallback={null}>
        {l === "hex" ? <HiveHex3D ref={stage} {...props} /> : <HiveStage3D ref={stage} look={l} {...props} />}
      </Suspense>
      <div className={`hd-meter${total.live ? " on" : ""}`}>
        <b>{fmtK(tokens)}</b>
        <span>hive tokens · {working > 0 ? `${working} of ${workers.length} working` : `${workers.length} workers idle`}{total.tokPerSec > 0.5 ? ` · ${Math.round(total.tokPerSec)} tok/s` : ""}</span>
      </div>
    </div>
  );
}
