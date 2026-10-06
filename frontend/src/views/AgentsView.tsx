import { translate, useI18n } from "../lib/i18n";
import { useSearchParams, useNavigate } from "react-router-dom";
import { PATHS } from "../lib/routes";
import { focusInWorld } from "../world/bus";
import React, { useCallback, useEffect, useLayoutEffect, useMemo, useRef, useState } from "react";
import { SquareIcon, ChevronRightIcon, PlusIcon, ArrowUpIcon, TerminalIcon, FolderIcon } from "lucide-react";
import "./coding.css";
import {
  CLIRunDetail,
  CLIRuns,
  CancelCLIRun,
  ExternalAgentsStatus,
  RemoteAgentNames,
  FollowUpCLIRun,
  StartCLIRun,
} from "../../wailsjs/go/app/App";
import { app } from "../../wailsjs/go/models";
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

/** "claude.mac" → { cli: "claude", host: "mac" }; a bare name runs on core. */
const partsOf = (n: string) => {
  const i = n.indexOf(".");
  return i < 0 ? { cli: n, host: "" } : { cli: n.slice(0, i), host: n.slice(i + 1) };
};
const whereOf = (n: string) => partsOf(n).host || "core";

/** The @word the caret is in, if any: same rule as the chat composer. */
function mentionAt(value: string, caret: number): { word: string; at: number } | null {
  const before = value.slice(0, caret);
  const at = before.lastIndexOf("@");
  if (at < 0 || (at > 0 && !/\s/.test(before[at - 1]))) return null;
  const word = before.slice(at + 1);
  return /^[\p{L}\p{N}._-]*$/u.test(word) ? { word, at } : null;
}

/** "@claude.mac fix the tests" → ["claude.mac", "fix the tests"]. */
function addressOf(value: string, names: string[]): [string, string] {
  const m = /^\s*@([\p{L}\p{N}._-]+)\s+/u.exec(value);
  if (m && names.includes(m[1])) return [m[1], value.slice(m[0].length)];
  return ["", value];
}

const LAST = "superai-coding-agent";
const lastAgent = () => {
  try { return localStorage.getItem(LAST) ?? ""; } catch { return ""; }
};

/**
 * Coding CLIs on core and on every linked agent, driven from here: start one
 * on a task, watch every tool call and what came back, answer its permission
 * prompts, send a follow-up into the same session, stop it. Runs started by
 * "@claude" in a conversation show up here too.
 */
export default function AgentsView() {
  const { t } = useI18n();
  const [params] = useSearchParams();
  const requestedRun = params.get("run") ?? "";
  const [runs, setRuns] = useState<Run[]>([]);
  const [selected, setSelected] = useState<string>("");
  const [events, setEvents] = useState<Record<string, Ev[]>>({});
  // Every coding CLI this core can start: its own ("claude") and each agent's ("claude.mac").
  const [choices, setChoices] = useState<string[] | null>(null);

  const load = useCallback(async () => {
    const [list, st, names] = await Promise.all([CLIRuns(), ExternalAgentsStatus(), RemoteAgentNames().catch(() => [])]);
    setRuns(list ?? []);
    const here = (st ?? []).filter((a) => a.installed).map((a) => a.name);
    const there = ((names ?? []) as { name?: string }[]).map((n) => n.name ?? "").filter((n) => n.includes("."));
    setChoices([...here, ...there]);
  }, []);

  useEffect(() => {
    void load().catch(error => { setChoices([]); toast.error(String(error)); });
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

  useEffect(() => {
    if (!requestedRun) return;
    const match = runs.find(run => run.id === requestedRun);
    if (match) setSelected(match.thread);
  }, [requestedRun, runs]);

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
    <div className="view cd">
      <aside className="cd-side">
        <div className="cd-side-h">
          <span>Coding</span>
          <button className={`cd-new${thread ? "" : " on"}`} onClick={() => setSelected("")}>
            <PlusIcon size={14} /> New run
          </button>
        </div>
        <div className="cd-list">
          {threads.length === 0 && <div className="cd-none">Runs you start, and ones the queen starts, show up here.</div>}
          {threads.map((t) => {
            const { cli, host } = partsOf(t.agent);
            return (
              <button key={t.id} className={`cd-item${t.id === selected ? " on" : ""}`} onClick={() => { setSelected(t.id); focusInWorld("machine", t.latest.remote || host || "core"); }}>
                <span className="cd-item-top">
                  <i className={`cd-dot ${t.latest.state}`} />
                  <b>{cli}</b>
                  <span>{host || "core"}</span>
                  {t.runs.length > 1 && <span>· {t.runs.length} turns</span>}
                  <time>{fromNow(parseTime(t.latest.started) ?? new Date())}</time>
                </span>
                <span className="cd-item-p">{t.runs[0].prompt}</span>
              </button>
            );
          })}
        </div>
      </aside>
      <main className="cd-main">
        {thread ? (
          <ThreadView thread={thread} events={events} />
        ) : (
          <Start
            choices={choices}
            onStarted={(r) => {
              setRuns((prev) => (prev.some((x) => x.id === r.id) ? prev : [r, ...prev]));
              setSelected(r.thread);
            }}
          />
        )}
      </main>
    </div>
  );
}

/** A new run: one box, @ picks which CLI and where. */
function Start({ choices, onStarted }: { choices: string[] | null; onStarted: (r: Run) => void }) {
  const navigate = useNavigate();
  const [text, setText] = useState("");
  const [cwd, setCwd] = useState("");
  const [ask, setAsk] = useState(true);
  const [busy, setBusy] = useState(false);
  const [fallback, setFallback] = useState(lastAgent);
  const box = useRef<HTMLTextAreaElement>(null);
  const names = choices ?? [];
  const [addressed, task] = addressOf(text, names);
  const agent = addressed || (names.includes(fallback) ? fallback : names[0] ?? "");

  // Grouped by machine: this is the answer to "what can run where".
  const machines = useMemo(() => {
    const by = new Map<string, string[]>();
    for (const n of names) by.set(whereOf(n), [...(by.get(whereOf(n)) ?? []), n]);
    return [...by.entries()].sort(([a], [b]) => (a === "core" ? -1 : b === "core" ? 1 : a.localeCompare(b)));
  }, [names]);

  const start = async () => {
    if (!task.trim() || !agent || busy) return;
    setBusy(true);
    try {
      const r = await StartCLIRun(agent, task.trim(), cwd, "", ask);
      try { localStorage.setItem(LAST, agent); } catch { /* remembered for this visit only */ }
      setFallback(agent);
      onStarted(r);
    } catch (e) {
      toast.error(String(e));
    } finally {
      setBusy(false);
    }
  };

  // Clicking a CLI addresses the message to it, keeping what was typed.
  const pick = (n: string) => {
    setFallback(n);
    try { localStorage.setItem(LAST,n); } catch { /* in-memory choice remains */ }
    const rest = addressOf(text, names)[1];
    const next = `@${n} ${rest}`;
    setText(next);
    requestAnimationFrame(() => { box.current?.focus(); box.current?.setSelectionRange(next.length, next.length); });
  };

  if (choices && choices.length === 0) {
    return (
      <div className="cd-start">
        <div className="cd-start-in">
          <h2>No coding CLI to run</h2>
          <p className="cd-sub">Core has none installed and no linked agent offers one. Install Claude Code or Codex on core, or run <code>superai agent</code> on a machine that has them.</p>
          <button className="cd-btn" onClick={() => navigate(PATHS.settings)}>Open Settings</button>
        </div>
      </div>
    );
  }

  return (
    <div className="cd-start">
      <div className="cd-start-in">
        <h2>{translate("New run")}</h2>
        <Composer
          boxRef={box}
          value={text}
          onChange={setText}
          names={names}
          placeholder={agent ? `Tell ${partsOf(agent).cli} on ${whereOf(agent)} what to do. @ for another CLI or machine` : "Loading…"}
          onSend={start}
          sendable={!!task.trim() && !!agent && !busy}
          foot={
            <>
              {agent && (
                <label className="cd-to" title={translate("Select a coding agent")}>
                  <TerminalIcon size={13} />
                  <select className="cd-agent-select" aria-label={translate("Select a coding agent")} value={agent} onChange={event => pick(event.target.value)}>
                    {machines.map(([host, list]) => <optgroup key={host} label={host === "core" ? "Core" : host}>
                      {list.map(name => <option key={name} value={name}>{partsOf(name).cli} · {whereOf(name)}</option>)}
                    </optgroup>)}
                  </select>
                </label>
              )}
              <label className="cd-cwd" title="The directory it works in, on that machine">
                <FolderIcon size={13} />
                <input value={cwd} placeholder={translate("workspace")} onChange={(e) => setCwd(e.target.value)} spellCheck={false} />
              </label>
              <label className="cd-ask" title="Off: the CLI runs its tools without asking">
                <input type="checkbox" checked={ask} onChange={(e) => setAsk(e.target.checked)} /> {translate("Ask before tools run")}
              </label>
            </>
          }
        />

      </div>
    </div>
  );
}

/**
 * The input both for a new run and for a follow-up: a textarea with a footer
 * of settings and the send button. With `names`, @ completes coding CLIs.
 */
function Composer({
  value, onChange, names = [], placeholder, onSend, sendable, disabled, foot, action, boxRef,
}: {
  value: string;
  onChange: (v: string) => void;
  names?: string[];
  placeholder: string;
  onSend: () => void;
  sendable: boolean;
  disabled?: boolean;
  foot?: React.ReactNode;
  action?: React.ReactNode;
  boxRef?: React.RefObject<HTMLTextAreaElement | null>;
}) {
  const own = useRef<HTMLTextAreaElement>(null);
  const ref = boxRef ?? own;
  const ime = useImeGuard();
  const [q, setQ] = useState<{ word: string; at: number } | null>(null);
  const [active, setActive] = useState(0);
  // Where the caret goes once a picked name is in: set in the same frame as
  // the new text, so nothing typed in between lands in front of it.
  const caretTo = useRef<number | null>(null);
  useLayoutEffect(() => {
    if (caretTo.current == null) return;
    ref.current?.focus();
    ref.current?.setSelectionRange(caretTo.current, caretTo.current);
    caretTo.current = null;
  }, [value, ref]);
  const matches = useMemo(() => {
    if (!q) return [];
    const w = q.word.toLowerCase();
    const hits = names.filter((n) => n.toLowerCase().startsWith(w) || whereOf(n).toLowerCase().startsWith(w));
    return hits.length === 1 && hits[0].toLowerCase() === w ? [] : hits;
  }, [q, names]);

  // WKWebView can deliver native input and composition commits without a
  // matching React change event. Keep the controlled draft in step with both.
  const syncInput = (input: HTMLTextAreaElement) => {
    onChange(input.value);
    setQ(names.length ? mentionAt(input.value, input.selectionStart ?? input.value.length) : null);
    setActive(0);
  };

  const accept = (n: string) => {
    if (!q) return;
    const caret = ref.current?.selectionStart ?? value.length;
    const ins = `@${n} `;
    const next = value.slice(0, q.at) + ins + value.slice(caret);
    caretTo.current = q.at + ins.length;
    onChange(next);
    setQ(null);
  };

  return (
    <div className={`cd-comp${disabled ? " off" : ""}`}>
      {matches.length > 0 && (
        <div className="cd-menu" role="listbox">
          {matches.map((n, i) => (
            <button
              key={n}
              role="option"
              aria-selected={i === active}
              className={i === active ? "on" : ""}
              onMouseDown={(e) => { e.preventDefault(); accept(n); }}
            >
              <b>@{n}</b>
              <span>{partsOf(n).cli} on {whereOf(n)}</span>
            </button>
          ))}
        </div>
      )}
      <textarea
        ref={ref}
        rows={2}
        value={value}
        placeholder={placeholder}
        disabled={disabled}
        aria-label={translate("Task instructions")}
        onInput={event => syncInput(event.currentTarget)}
        onChange={event => syncInput(event.currentTarget)}
        onBlur={() => setQ(null)}
        onCompositionStart={ime.handlers.onCompositionStart}
        onCompositionEnd={event => { ime.handlers.onCompositionEnd(event); syncInput(event.currentTarget); }}
        onKeyDown={(e) => {
          if (matches.length > 0) {
            if (e.key === "ArrowDown" || e.key === "ArrowUp") {
              e.preventDefault();
              setActive((i) => (i + (e.key === "ArrowDown" ? 1 : matches.length - 1)) % matches.length);
              return;
            }
            if ((e.key === "Enter" || e.key === "Tab") && !ime.composing(e)) {
              e.preventDefault();
              accept(matches[active]);
              return;
            }
            if (e.key === "Escape") { e.preventDefault(); setQ(null); return; }
          }
          if (e.key === "Enter" && !e.shiftKey && !ime.composing(e)) {
            e.preventDefault();
            if (sendable) onSend();
          }
        }}
      />
      <div className="cd-comp-f">
        {foot}
        <span className="cd-grow" />
        {action ?? (
          <button className="cd-send" onClick={onSend} disabled={!sendable} aria-label="Send">
            <ArrowUpIcon size={16} />
          </button>
        )}
      </div>
    </div>
  );
}

function ThreadView({ thread, events }: { thread: Thread; events: Record<string, Ev[]> }) {
  const [follow, setFollow] = useState("");
  const [sending, setSending] = useState(false);
  const end = useRef<HTMLDivElement>(null);
  const running = thread.latest.state === "running";
  const total = thread.runs.reduce((n, r) => n + r.costUsd, 0);
  const count = thread.runs.reduce((n, r) => n + (events[r.id]?.length ?? 0), 0);
  const { cli, host } = partsOf(thread.agent);

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
    <div className="cd-thread">
      <div className="cd-thread-h">
        <TerminalIcon size={15} />
        <b>{cli}</b>
        <span>on {host || "core"}</span>
        <span className="cd-path" title={thread.latest.cwd}>{shortPath(thread.latest.cwd)}</span>
        <span className="cd-meta">
          {thread.latest.ask ? "asks before tools" : "unattended"}
          {total > 0 && ` · ${cost(total)}`}
        </span>
      </div>
      <div className="cd-log">
        <div className="cd-log-in">
          {thread.runs.map((r) => (
            <RunBlock key={r.id} run={r} events={events[r.id] ?? []} />
          ))}
          <div ref={end} />
        </div>
      </div>
      <div className="cd-follow">
        <Composer
          value={follow}
          onChange={setFollow}
          placeholder={running ? `${cli} is working…` : `Continue with ${cli}`}
          onSend={send}
          sendable={!running && !sending && !!follow.trim()}
          disabled={running}
          action={
            running ? (
              <button className="cd-send stop" onClick={() => CancelCLIRun(thread.latest.id)} aria-label="Stop" title="Stop">
                <SquareIcon size={12} fill="currentColor" />
              </button>
            ) : undefined
          }
        />
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
