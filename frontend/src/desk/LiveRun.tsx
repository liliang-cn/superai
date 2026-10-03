import React, { useEffect, useRef, useState } from "react";
import { CLIRunDetail } from "../../wailsjs/go/app/App";
import { EventsOn } from "../../wailsjs/runtime";
import { elapsed } from "../canvas/data";
import { Response } from "@/components/ai-elements/response";

export interface RunEvent {
  seq: number;
  at: string;
  kind: "text" | "tool" | "result" | "note" | "error" | string;
  text?: string;
  tool?: string;
  detail?: string;
  failed?: boolean;
}

export interface RunDetail {
  id: string;
  agent: string;
  prompt: string;
  cwd: string;
  session?: string;
  remote?: string;
  state: string;
  started: string;
  ended?: string;
  summary?: string;
  error?: string;
  events?: RunEvent[];
}

/** One coding run, followed as it happens: loaded once, then kept current by
 *  cli:event for its steps and cli:run for its state. */
export function useRun(id: string): RunDetail | null {
  const [run, setRun] = useState<RunDetail | null>(null);
  useEffect(() => {
    if (!id) return;
    let alive = true;
    CLIRunDetail(id).then((r) => { if (alive) setRun(r as unknown as RunDetail); }).catch(() => {});
    const offEv = EventsOn("cli:event", (p: { run?: string; event?: RunEvent }) => {
      if (p?.run !== id || !p.event) return;
      setRun((cur) => {
        if (!cur) return cur;
        const evs = cur.events ?? [];
        if (evs.some((e) => e.seq === p.event!.seq)) return cur;
        return { ...cur, events: [...evs, p.event!] };
      });
    });
    const offRun = EventsOn("cli:run", (p: Partial<RunDetail>) => {
      if (p?.id !== id) return;
      setRun((cur) => (cur ? { ...cur, ...p, events: cur.events } : cur));
    });
    return () => {
      alive = false;
      if (typeof offEv === "function") offEv();
      if (typeof offRun === "function") offRun();
    };
  }, [id]);
  return run;
}

const firstLine = (s = "") => s.split("\n").find((l) => l.trim())?.trim() ?? "";

/** The run as a terminal reads: what was asked, then every step. */
export default function LiveRun({ run, now }: { run: RunDetail | null; now: Date }) {
  const end = useRef<HTMLDivElement>(null);
  const n = run?.events?.length ?? 0;
  useEffect(() => { end.current?.scrollIntoView({ block: "end" }); }, [n]);
  if (!run) return <div className="dk-live-empty">Opening the run…</div>;
  const live = run.state === "running";
  return (
    <div className="dk-term">
      <div className="dk-term-bar">
        <b>{run.agent.includes(".") || !run.remote ? run.agent : `${run.agent}.${run.remote}`}</b>
        <span className="dk-term-cwd">{run.cwd}</span>
        <span className="dk-term-time">{live ? elapsed(run.started, now.getTime()) : run.state}</span>
      </div>
      <div className="dk-term-body">
        <div className="dk-ask">&gt; {run.prompt}</div>
        {(run.events ?? []).map((e) => {
          switch (e.kind) {
            case "tool":
              return <div key={e.seq} className="dk-step"><i>●</i> <b>{e.tool}</b> {firstLine(e.detail)}</div>;
            case "result":
              return <div key={e.seq} className={e.failed ? "dk-res bad" : "dk-res"}>{firstLine(e.text || e.detail).slice(0, 200)}</div>;
            case "error":
              return <div key={e.seq} className="dk-res bad">{e.text}</div>;
            case "note":
              return <div key={e.seq} className="dk-note">{e.text}</div>;
            default:
              return e.text ? <div key={e.seq} className="dk-say">{e.text}</div> : null;
          }
        })}
        {!live && run.summary && !(run.events ?? []).some((e) => e.kind === "text" && e.text?.trim() === run.summary?.trim()) && <div className="dk-say done"><Response>{run.summary}</Response></div>}
        {!live && run.error && <div className="dk-res bad">{run.error}</div>}
        {live && <span className="dk-cursor" />}
        <div ref={end} />
      </div>
    </div>
  );
}
