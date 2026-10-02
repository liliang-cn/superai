import React, { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { SquareIcon, CornerDownLeftIcon, ChevronRightIcon } from "lucide-react";
import {
  CLIRunDetail,
  CLIRuns,
  CancelCLIRun,
  ExternalAgentsStatus,
  FollowUpCLIRun,
  StartCLIRun,
} from "../../wailsjs/go/app/App";
import { app, backend } from "../../wailsjs/go/models";
import { EventsOn } from "../../wailsjs/runtime";
import { fromNow, parseTime } from "../lib/format";
import { useImeGuard } from "@/lib/ime";
import { toast } from "../lib/toasts";
import ReactMarkdown from "react-markdown";
import remarkGfm from "remark-gfm";
import { mdComponents } from "../components/Transcript";

// Plain shapes: what arrives over events is JSON, not the generated classes.
type Ev = Omit<app.CLIRunEvent, "convertValues">;
type Run = Omit<app.CLIRun, "convertValues" | "events"> & { events?: Ev[] };

/** A conversation with one CLI: the first run and its follow-ups. */
interface Thread {
  id: string;
  agent: string;
  runs: Run[]; // oldest first
  latest: Run;
}

function threadsOf(runs: Run[]): Thread[] {
  const by = new Map<string, Run[]>();
  for (const r of runs) {
    const list = by.get(r.thread) ?? [];
    list.push(r);
    by.set(r.thread, list);
  }
  const out: Thread[] = [];
  for (const [id, list] of by) {
    list.sort((a, b) => +parseTime(a.started)! - +parseTime(b.started)!);
    out.push({ id, agent: list[0].agent, runs: list, latest: list[list.length - 1] });
  }
  out.sort((a, b) => +parseTime(b.latest.started)! - +parseTime(a.latest.started)!);
  return out;
}

/** The last two parts of a path: enough to know which project. */
const shortPath = (p: string) => {
  const parts = p.split("/").filter(Boolean);
  return parts.length > 2 ? "…/" + parts.slice(-2).join("/") : p;
};

const cost = (n: number) => (n > 0 ? `$${n.toFixed(n < 1 ? 3 : 2)}` : "");

/**
 * Agent CLIs on this machine, driven from here: start Claude Code or Codex on
 * a task, watch every tool call and what came back, answer its permission
 * prompts on the approval cards, send a follow-up into the same session, stop
 * it. Runs started by "@claude" in a conversation show up here too.
 */
export default function AgentsView() {
  const [runs, setRuns] = useState<Run[]>([]);
  const [agents, setAgents] = useState<backend.ExternalAgentStatus[]>([]);
  const [selected, setSelected] = useState<string>("");
  const [events, setEvents] = useState<Record<string, Ev[]>>({});
  const [composing, setComposing] = useState(false);

  const load = useCallback(async () => {
    const [list, st] = await Promise.all([CLIRuns(), ExternalAgentsStatus()]);
    setRuns(list ?? []);
    setAgents((st ?? []).filter((a) => a.installed));
  }, []);

  useEffect(() => {
    load();
    const offRun = EventsOn("cli:run", (p: Run) => {
      setRuns((prev) => {
        const i = prev.findIndex((r) => r.id === p.id);
        if (i < 0) return [p, ...prev];
        const next = [...prev];
        next[i] = { ...prev[i], ...p };
        return next;
      });
    });
    const offEv = EventsOn("cli:event", (p: { run: string; event: Ev }) => {
      setEvents((prev) => {
        const list = prev[p.run];
        if (!list) return prev; // not opened yet; the detail load brings it
        if (list.some((e) => e.seq === p.event.seq)) return prev;
        return { ...prev, [p.run]: [...list, p.event] };
      });
    });
    return () => {
      offRun();
      offEv();
    };
  }, [load]);

  const threads = useMemo(() => threadsOf(runs), [runs]);
  const thread = threads.find((t) => t.id === selected) ?? null;

  // Load each run of the open thread once; live events extend it after.
  useEffect(() => {
    if (!thread) return;
    for (const r of thread.runs) {
      if (events[r.id]) continue;
      setEvents((prev) => (prev[r.id] ? prev : { ...prev, [r.id]: [] }));
      CLIRunDetail(r.id).then((d) =>
        setEvents((prev) => {
          const live = prev[r.id] ?? [];
          const have = new Set((d.events ?? []).map((e) => e.seq));
          return { ...prev, [r.id]: [...(d.events ?? []), ...live.filter((e) => !have.has(e.seq))] };
        }),
      );
    }
  }, [thread, events]);

  return (
    <div className="view agents-view">
      <div className="view-header with-action">
        <div>
          <div className="view-title">Agents</div>
          <div className="view-desc">
            {agents.length > 0
              ? `Claude Code, Codex and the other agent CLIs on this machine. Type @${agents[0].name} in a conversation, or start one here.`
              : "No agent CLI found. Install Claude Code or Codex, and switch on External agents in Settings."}
          </div>
        </div>
        <div className="vh-actions">
          <button className="btn sm" disabled={agents.length === 0} onClick={() => setComposing((v) => !v)}>
            {composing ? "Close" : "New run"}
          </button>
        </div>
      </div>

      {composing && (
        <NewRun
          agents={agents}
          onStarted={(r) => {
            setComposing(false);
            setRuns((prev) => (prev.some((x) => x.id === r.id) ? prev : [r, ...prev]));
            setSelected(r.thread);
          }}
        />
      )}

      <div className="agents-split">
        <div className="agents-list">
          {threads.length === 0 && <div className="inline-empty"><div className="ie-hint">No runs yet.</div></div>}
          {threads.map((t) => (
            <button
              key={t.id}
              className={`agents-item${t.id === selected ? " on" : ""}`}
              onClick={() => setSelected(t.id)}
            >
              <span className={`status-dot ${dot(t.latest.state)}`} />
              <span className="ai-body">
                <span className="ai-top">
                  <span className="ai-agent">@{t.agent}</span>
                  {t.runs.length > 1 && <span className="ai-turns">{t.runs.length} turns</span>}
                  <span className="ai-when">{fromNow(parseTime(t.latest.started) ?? new Date())}</span>
                </span>
                <span className="ai-prompt">{t.runs[0].prompt}</span>
              </span>
            </button>
          ))}
        </div>
        <div className="agents-detail">
          {thread ? (
            <ThreadView thread={thread} events={events} />
          ) : (
            <div className="inline-empty">
              <div className="ie-hint">Pick a run to see what it did.</div>
            </div>
          )}
        </div>
      </div>
    </div>
  );
}

function dot(state: string) {
  return state === "running" ? "live" : state === "done" ? "ok" : state === "failed" ? "bad" : "unknown";
}

function NewRun({ agents, onStarted }: { agents: backend.ExternalAgentStatus[]; onStarted: (r: Run) => void }) {
  const [agent, setAgent] = useState(agents[0]?.name ?? "claude");
  const [prompt, setPrompt] = useState("");
  const [cwd, setCwd] = useState("");
  const [ask, setAsk] = useState(true);
  const [busy, setBusy] = useState(false);
  const ime = useImeGuard();

  const start = async () => {
    if (!prompt.trim() || busy) return;
    setBusy(true);
    try {
      const r = await StartCLIRun(agent, prompt, cwd, "", ask);
      onStarted(r);
    } catch (e) {
      toast.error(String(e));
    } finally {
      setBusy(false);
    }
  };

  return (
    <div className="card agents-new">
      <div className="agents-new-row">
        <div className="seg">
          {agents.map((a) => (
            <button key={a.name} className={`seg-btn${a.name === agent ? " on" : ""}`} onClick={() => setAgent(a.name)}>
              {a.name}
            </button>
          ))}
        </div>
        <input
          className="input"
          placeholder="Directory (blank: the workspace)"
          value={cwd}
          onChange={(e) => setCwd(e.target.value)}
        />
        <label className="agents-ask" title="Off: the CLI runs its tools without asking">
          <input type="checkbox" checked={ask} onChange={(e) => setAsk(e.target.checked)} /> Ask before tools run
        </label>
      </div>
      <textarea
        className="input agents-prompt"
        placeholder={`What should ${agent} do?`}
        value={prompt}
        rows={3}
        onChange={(e) => setPrompt(e.target.value)}
        {...ime.handlers}
        onKeyDown={(e) => {
          if (e.key === "Enter" && (e.metaKey || e.ctrlKey) && !ime.composing(e)) start();
        }}
      />
      <div className="agents-new-row end">
        <button className="btn" onClick={start} disabled={!prompt.trim() || busy}>
          {busy ? "Starting…" : `Start ${agent}`}
        </button>
      </div>
    </div>
  );
}

function ThreadView({ thread, events }: { thread: Thread; events: Record<string, Ev[]> }) {
  const [follow, setFollow] = useState("");
  const [sending, setSending] = useState(false);
  const end = useRef<HTMLDivElement>(null);
  const running = thread.latest.state === "running";
  const ime = useImeGuard();
  const total = thread.runs.reduce((n, r) => n + r.costUsd, 0);
  const count = thread.runs.reduce((n, r) => n + (events[r.id]?.length ?? 0), 0);

  useEffect(() => {
    end.current?.scrollIntoView({ block: "end" });
  }, [count, thread.id]);

  const send = async () => {
    if (!follow.trim() || sending || running) return;
    setSending(true);
    try {
      await FollowUpCLIRun(thread.latest.id, follow);
      setFollow("");
    } catch (e) {
      toast.error(String(e));
    } finally {
      setSending(false);
    }
  };

  return (
    <div className="agents-thread">
      <div className="agents-thread-head">
        <span className="ai-agent">@{thread.agent}</span>
        <span className="agents-cwd" title={thread.latest.cwd}>{shortPath(thread.latest.cwd)}</span>
        <span className="agents-meta">
          {thread.latest.ask ? "asks" : "unattended"}
          {total > 0 && ` · ${cost(total)}`}
        </span>
        {running && (
          <button className="btn ghost sm" onClick={() => CancelCLIRun(thread.latest.id)}>
            <SquareIcon size={12} /> Stop
          </button>
        )}
      </div>
      <div className="agents-log">
        {thread.runs.map((r) => (
          <RunBlock key={r.id} run={r} events={events[r.id] ?? []} />
        ))}
        <div ref={end} />
      </div>
      <div className="agents-follow">
        <textarea
          className="input"
          rows={2}
          placeholder={running ? `${thread.agent} is working…` : `Continue with ${thread.agent}`}
          value={follow}
          disabled={running}
          onChange={(e) => setFollow(e.target.value)}
          {...ime.handlers}
          onKeyDown={(e) => {
            if (e.key === "Enter" && !e.shiftKey && !ime.composing(e)) {
              e.preventDefault();
              send();
            }
          }}
        />
        <button className="btn" onClick={send} disabled={running || sending || !follow.trim()} aria-label="Send">
          <CornerDownLeftIcon size={15} />
        </button>
      </div>
    </div>
  );
}

function RunBlock({ run, events }: { run: Run; events: Ev[] }) {
  // A result is drawn under the call it answers.
  const results = new Map<string, Ev>();
  for (const e of events) if (e.kind === "result" && e.callId) results.set(e.callId, e);
  const shown = events.filter((e) => !(e.kind === "result" && e.callId && events.some((c) => c.kind === "tool" && c.callId === e.callId)));
  const secs = run.ended ? (+parseTime(run.ended)! - +parseTime(run.started)!) / 1000 : 0;

  return (
    <div className="agents-run">
      <div className="agents-you">{run.prompt}</div>
      {shown.map((e) =>
        e.kind === "tool" ? (
          <ToolStep key={e.seq} call={e} result={e.callId ? results.get(e.callId) : undefined} />
        ) : e.kind === "text" ? (
          <div key={e.seq} className="agents-text md">
            <ReactMarkdown remarkPlugins={[remarkGfm]} components={mdComponents}>
              {e.text ?? ""}
            </ReactMarkdown>
          </div>
        ) : e.kind === "note" ? (
          <div key={e.seq} className={`agents-note${e.failed ? " bad" : ""}`}>
            {e.tool && <b>{e.tool}</b>} {e.text}
          </div>
        ) : e.kind === "error" ? (
          <div key={e.seq} className="agents-note bad">{e.text}</div>
        ) : (
          <ToolStep key={e.seq} call={{ ...e, kind: "tool", detail: "" } as Ev} result={e} />
        ),
      )}
      <div className={`agents-end ${run.state}`}>
        {run.state === "running" ? (
          <><span className="spinner" style={{ borderTopColor: "var(--accent)" }} /> working</>
        ) : (
          <>
            {run.state}
            {secs > 0 && ` · ${secs.toFixed(1)}s`}
            {` · ${run.tools} tool calls`}
            {run.costUsd > 0 && ` · ${cost(run.costUsd)}`}
            {run.error && ` · ${run.error}`}
          </>
        )}
      </div>
    </div>
  );
}

function ToolStep({ call, result }: { call: Ev; result?: Ev }) {
  const [open, setOpen] = useState(false);
  const out = result?.text ?? "";
  return (
    <div className={`agents-tool${result?.failed ? " bad" : ""}${result ? "" : " live"}`}>
      <button className="agents-tool-head" onClick={() => setOpen((v) => !v)} disabled={!out}>
        <ChevronRightIcon size={13} className={open ? "rot" : ""} style={{ opacity: out ? 1 : 0.25 }} />
        <span className="agents-tool-name">{call.tool}</span>
        <span className="agents-tool-detail">{call.detail}</span>
      </button>
      {open && out && <pre className="agents-tool-out">{out}</pre>}
    </div>
  );
}
