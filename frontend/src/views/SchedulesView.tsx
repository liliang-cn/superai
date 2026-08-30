import React, { useCallback, useEffect, useRef, useState } from "react";
import { SquareIcon } from "lucide-react";
import {
  CancelScheduledRun,
  DeleteScheduledPrompt,
  RunScheduledPromptNow,
  ScheduleFromText,
  ScheduledPrompts,
  SetScheduledPromptEnabled,
} from "../../wailsjs/go/main/App";
import { agent } from "../../wailsjs/go/models";
import { AppStatus } from "../lib/types";
import { describeCron } from "../lib/cron";
import { firstLine, formatStamp, fromNow, parseTime } from "../lib/format";
import { ScheduleRunLog } from "../lib/useScheduleRuns";
import { ScheduleRunList } from "../components/ScheduleRuns";
import { notifyNow, notifyPermission, requestNotifyPermission } from "../lib/notify";
import { useImeGuard } from "@/lib/ime";

type Note = { kind: "ok" | "err"; text: string } | null;

/**
 * Scheduled prompts: what SuperAI will do on its own, and what it did.
 *
 * Every mutating binding answers with a message string rather than throwing, and
 * the message is the whole point — the backend refuses a schedule it cannot parse
 * and says why, instead of accepting it and silently never firing. So each action
 * routes its reply into the note line, "ok" included.
 */
export default function SchedulesView({
  status,
  log,
  onOpenConversation,
  embedded = false,
  onCount,
}: {
  status: AppStatus | null;
  log: ScheduleRunLog;
  onOpenConversation: (session: string) => void;
  /** Rendered as a tab inside Records: drop the page chrome, keep the actions.
   *  The tab and the page around it already say what this is. */
  embedded?: boolean;
  /** Report the list length so the tab that holds this can count it. Only the
   *  scheduler knows the real number; Life() reports its own view of the same
   *  thing and the two would drift. */
  onCount?: (n: number) => void;
}) {
  const ime = useImeGuard();
  const [list, setList] = useState<agent.ScheduledPrompt[]>([]);
  const [loading, setLoading] = useState(true);
  const [note, setNote] = useState<Note>(null);
  const [composing, setComposing] = useState(false);
  const [prompt, setPrompt] = useState("");
  // What the agent said about what it arranged. Shown verbatim: it is the only
  // account of how a sentence was understood.
  const [reply, setReply] = useState("");
  const [saving, setSaving] = useState(false);
  const [busy, setBusy] = useState("");
  const [confirmDelete, setConfirmDelete] = useState("");
  const [fresh, setFresh] = useState("");
  // Whether this browser will draw a banner when a run finishes while the page
  // is in the background. Read once: it only changes when the button below is
  // pressed, or in browser settings, which reloads anyway.
  const [banners, setBanners] = useState(notifyPermission);

  const load = useCallback(async (quiet = false) => {
    if (!quiet) setLoading(true);
    try {
      const res = await ScheduledPrompts();
      setList(Array.isArray(res) ? res : []);
    } catch (e: any) {
      if (!quiet) setNote({ kind: "err", text: String(e?.message || e) });
    } finally {
      if (!quiet) setLoading(false);
    }
  }, []);

  useEffect(() => {
    load();
  }, [load]);

  // "Run now" returns as soon as the run has started, so the row's state comes
  // from the backend's own `running` flag rather than from the click. Poll only
  // while something is in flight: a schedule list at rest changes once an hour,
  // and a spinner that never stops is worse than no spinner at all.
  const anyRunning = list.some((s) => s.running);
  useEffect(() => {
    if (!anyRunning) return;
    const t = window.setInterval(() => load(true), 1500);
    return () => window.clearInterval(t);
  }, [anyRunning, load]);

  // Being on this page is what "I have read the runs" means, so the sidebar
  // badge and any toasts clear on arrival and stay clear while it is open.
  useEffect(() => {
    if (log.unseen > 0 || log.toasts.length > 0) log.acknowledge();
  }, [log]);

  // A run that just finished changed last_run, and may have moved next_run on.
  // Keyed on the newest run rather than the count, which stops changing once the
  // log is full.
  const newestRun = log.runs.length > 0 ? log.runs[0].key : "";
  const refreshedFor = useRef(newestRun);
  useEffect(() => {
    if (newestRun === refreshedFor.current) return;
    refreshedFor.current = newestRun;
    load();
  }, [newestRun, load]);

  useEffect(() => {
    onCount?.(list.length);
  }, [list.length, onCount]);

  const canCreate = prompt.trim() !== "" && !saving;

  /**
   * Hand the sentence to the agent and let it arrange the schedule.
   *
   * Nothing here parses time. "每天早八点" and "工作日下午三点" are the same kind
   * of thing to a model and were six different controls to a form, and the
   * agent already owns the tools that write a schedule.
   */
  const create = async () => {
    setSaving(true);
    setNote(null);
    setReply("");
    const known = new Set(list.map((s) => s.id));
    try {
      const res: any = await ScheduleFromText(prompt.trim());
      if (res && res.answer) setReply(String(res.answer));
      if (res && res.ok === false) {
        setNote({ kind: "err", text: String(res.error || "could not arrange that") });
        return;
      }
      const next = await ScheduledPrompts();
      const rows = Array.isArray(next) ? next : [];
      setList(rows);
      // The new row is the confirmation, not the reply: a model that says it
      // scheduled something and a scheduler that has it are different claims.
      const created = rows.find((s) => !known.has(s.id));
      setFresh(created?.id || "");
      if (created) {
        const at = parseTime(created.next_run);
        setNote({
          kind: "ok",
          text: at ? `Scheduled. First run ${formatStamp(at)} — ${fromNow(at)}.` : "Scheduled.",
        });
        setPrompt("");
        setComposing(false);
      } else {
        setNote({
          kind: "err",
          text: "Nothing new is on the schedule — read the reply above and try saying the time more plainly.",
        });
      }
    } catch (e: any) {
      setNote({ kind: "err", text: String(e?.message || e) });
    } finally {
      setSaving(false);
    }
  };

  // opts.refusalIsFine covers Stop: a stop that lands after the run has already
  // finished is late, not wrong, and the backend says so in words. Painting
  // that reply red would teach the user that the button is broken.
  const act = async (
    id: string,
    label: string,
    call: () => Promise<string>,
    opts: { refusalIsFine?: boolean } = {},
  ) => {
    setBusy(id);
    setNote(null);
    setFresh("");
    setConfirmDelete("");
    try {
      const res = await call();
      if (res === "ok") setNote({ kind: "ok", text: `${label} done.` });
      else setNote({ kind: opts.refusalIsFine ? "ok" : "err", text: res });
    } catch (e: any) {
      setNote({ kind: "err", text: String(e?.message || e) });
    } finally {
      setBusy("");
      load();
    }
  };

  // A missing "scheduler" key is read as running, so a backend that stops
  // reporting it cannot produce a false alarm here.
  const schedulerDown = status !== null && status.ready && !status.scheduler;

  const actions = (
    <div className="vh-actions">
      {/* Asked for from a click, never on load: an unprompted permission
          dialog is what teaches people to press "block", and a blocked
          notification cannot be asked for again from script. */}
      {banners === "default" && (
        <button
          className="btn ghost sm"
          title="Show a system notification when a scheduled run finishes while this tab is in the background"
          onClick={async () => {
            const granted = await requestNotifyPermission();
            setBanners(granted);
            // One banner the moment it is allowed. Being told "notifications
            // are on" is not the same as having seen one arrive, and the
            // difference is where Focus modes and full-screen apps hide.
            if (granted === "granted") {
              notifyNow("SuperAI", "通知已开启 —— 定时任务跑完时会这样提醒你。");
            }
          }}
        >
          🔔 Enable notifications
        </button>
      )}
      {banners === "granted" && (
        <button
          className="btn ghost sm"
          title="Post a test notification now"
          onClick={() => {
            const result = notifyNow("SuperAI", "这是一条测试通知。定时任务跑完时就是这个样子。");
            setNote(
              result === "posted"
                ? {
                    kind: "ok",
                    text: "已发出一条测试通知。没看见的话，多半是系统的「专注模式」把它收进了通知中心。",
                  }
                : { kind: "err", text: `通知没能发出（${result}）。` },
            );
          }}
        >
          🔔 试一下
        </button>
      )}
      <button
        className="btn ghost sm"
        onClick={() => {
          setFresh("");
          setComposing((v) => !v);
        }}
      >
        {composing ? "Close" : "+ New schedule"}
      </button>
      {/* Wrapped rather than passed directly: onClick would hand load()
          the click event as its `quiet` argument. */}
      <button className="btn ghost sm" onClick={() => load()} disabled={loading}>
        {loading ? (
          <>
            <span className="spinner" style={{ borderTopColor: "var(--text-1)" }} /> Loading…
          </>
        ) : (
          "↻ Refresh"
        )}
      </button>
    </div>
  );

  const body = (
    <>
        {composing && (
          <div className="card" style={{ marginBottom: 14 }}>
            <div className="card-title">New schedule</div>
            <div className="card-desc">
              Say what SuperAI should do and when, in one sentence. It works out the timing itself
              and the answers land in a conversation you can open afterwards.
            </div>
            <div className="field">
              <textarea
                className="input"
                rows={3}
                autoFocus
                value={prompt}
                placeholder="每天早八点看看昨天的部署有没有问题，有问题就告诉我"
                onChange={(e) => setPrompt(e.target.value)}
                onCompositionStart={ime.handlers.onCompositionStart}
                onCompositionEnd={ime.handlers.onCompositionEnd}
                onKeyDown={(e) => {
                  if (ime.composing(e)) return;
                  if ((e.metaKey || e.ctrlKey) && e.key === "Enter" && canCreate) create();
                }}
              />
              <span className="hint">
                一句话说清楚做什么、什么时候做。⌘/Ctrl + Enter 提交。
              </span>
            </div>

            {reply && <div className="card-desc" style={{ whiteSpace: "pre-wrap" }}>{reply}</div>}

            <div style={{ display: "flex", alignItems: "center", gap: 12 }}>
              <button className="btn" onClick={create} disabled={!canCreate}>
                {saving ? (
                  <>
                    <span className="spinner" /> Arranging…
                  </>
                ) : (
                  "Schedule it"
                )}
              </button>
              {prompt.trim() === "" && (
                <span className="save-note">Say what should happen, and when.</span>
              )}
            </div>
          </div>
        )}

        {banners === "denied" && (
          <div className="save-note" style={{ display: "block", marginBottom: 12 }}>
            通知被浏览器挡住了。定时任务照常运行，但页面在后台时不会弹提示 —— 要开的话，
            在地址栏左边的站点设置里把「通知」改成允许。
          </div>
        )}

        {note && (
          <div className={`save-note ${note.kind}`} style={{ display: "block", marginBottom: 12 }}>
            {note.kind === "ok" ? "✓" : "⚠"} {note.text}
          </div>
        )}

        {schedulerDown && (
          <div className="report-error" style={{ marginBottom: 12 }}>
            ⚠ The scheduler is not running{status?.schedulerError ? ` — ${status.schedulerError}` : ""}.
            Existing schedules are listed but nothing will fire.
          </div>
        )}

        {loading && list.length === 0 && (
          <div className="loading-row">
            <span className="spinner" style={{ borderTopColor: "var(--accent)" }} /> Loading
            schedules…
          </div>
        )}

        {!loading && list.length === 0 && (
          <div className="inline-empty">
            <div className="ie-icon">⏰</div>
            <div>Nothing is scheduled.</div>
            <div className="ie-hint">
              Use “+ New schedule” to have SuperAI do something every morning, every weekday, or
              every few hours.
            </div>
          </div>
        )}

        {list.length > 0 && (
          <div className="record-list">
            {list.map((s) => {
              const next = parseTime(s.next_run);
              const last = parseTime(s.last_run);
              const words = describeCron(s.schedule);
              const title = (s.note || "").trim() || firstLine(s.prompt, 70);
              // Two different things, kept apart on purpose: `acting` is a
              // binding call in flight (milliseconds), `running` is the agent
              // turn itself (minutes). Conflating them is what made "Run now"
              // look finished the moment the click returned.
              const acting = busy === s.id;
              const running = !!s.running;
              return (
                <div
                  className={`record-card${s.enabled ? "" : " paused"}${
                    fresh === s.id ? " fresh" : ""
                  }`}
                  key={s.id}
                >
                  <div className="rc-title" style={{ display: "flex", alignItems: "center", gap: 8 }}>
                    <span className={`status-dot ${s.enabled ? "ok" : "unknown"}`} />
                    <span className="sched-name">{title}</span>
                    {!s.enabled && <span className="chip">paused</span>}
                    {running && (
                      <span className="chip">
                        <span className="spinner" style={{ borderTopColor: "var(--text-1)" }} />{" "}
                        running
                      </span>
                    )}
                    <span className="sched-actions">
                      {/* Run now is the only way to find out whether a schedule
                          does what was meant without waiting for the hour. It
                          starts the run and returns; while the run is in flight
                          the same button stops it, the way the composer's send
                          button becomes stop mid-answer. */}
                      {running ? (
                        <button
                          className="btn ghost sm"
                          disabled={acting}
                          title="Stop this run"
                          onClick={() =>
                            act(s.id, "Stop", () => CancelScheduledRun(s.id), {
                              refusalIsFine: true,
                            })
                          }
                        >
                          <SquareIcon className="size-3 fill-current" /> Stop
                        </button>
                      ) : (
                        <button
                          className="btn ghost sm"
                          disabled={acting}
                          title="Run this prompt immediately"
                          onClick={() => act(s.id, "Run", () => RunScheduledPromptNow(s.id))}
                        >
                          {acting ? (
                            <>
                              <span
                                className="spinner"
                                style={{ borderTopColor: "var(--text-1)" }}
                              />{" "}
                              Starting…
                            </>
                          ) : (
                            "Run now"
                          )}
                        </button>
                      )}
                      <button
                        className="btn ghost sm"
                        disabled={acting}
                        onClick={() =>
                          act(s.id, s.enabled ? "Pause" : "Resume", () =>
                            SetScheduledPromptEnabled(s.id, !s.enabled),
                          )
                        }
                      >
                        {s.enabled ? "Pause" : "Resume"}
                      </button>
                      <button
                        className="btn ghost sm"
                        disabled={acting}
                        onClick={() => {
                          if (confirmDelete !== s.id) {
                            setConfirmDelete(s.id);
                            return;
                          }
                          setConfirmDelete("");
                          act(s.id, "Delete", () => DeleteScheduledPrompt(s.id));
                        }}
                      >
                        {confirmDelete === s.id ? "Click again to delete" : "Delete"}
                      </button>
                    </span>
                  </div>
                  <div className="rc-row">
                    <span className="rc-key">When</span>
                    <span className="rc-val">
                      {words !== "" && <>{words} </>}
                      <span className="mono">{s.schedule}</span>
                    </span>
                  </div>
                  <div className="rc-row">
                    <span className="rc-key">Next run</span>
                    <span className="rc-val">
                      {!s.enabled
                        ? "— paused"
                        : next
                          ? `${formatStamp(next)} · ${fromNow(next)}`
                          : "unknown — “Run now” tests it without waiting"}
                    </span>
                  </div>
                  {last && (
                    <div className="rc-row">
                      <span className="rc-key">Last run</span>
                      <span className="rc-val">
                        {formatStamp(last)} · {fromNow(last)}
                      </span>
                    </div>
                  )}
                  {title.trim() !== s.prompt.trim() && (
                    <div className="rc-row">
                      <span className="rc-key">Prompt</span>
                      <span className="rc-val">{s.prompt}</span>
                    </div>
                  )}
                  {!!s.session && (
                    <div className="rc-row">
                      <span className="rc-key">Conversation</span>
                      <span className="rc-val">
                        <button className="link-btn" onClick={() => onOpenConversation(s.session!)}>
                          {s.session}
                        </button>
                      </span>
                    </div>
                  )}
                </div>
              );
            })}
          </div>
        )}

      <ScheduleRunList log={log} onOpenConversation={onOpenConversation} />
    </>
  );

  if (embedded) {
    return (
      <>
        <div className="tab-actions">{actions}</div>
        {body}
      </>
    );
  }

  return (
    <div className="view">
      <div className="view-header with-action">
        <div>
          <div className="view-title">Schedules{list.length > 0 ? ` (${list.length})` : ""}</div>
          <div className="view-desc">
            Prompts SuperAI runs on a clock, without being asked — “every morning at eight, work out
            my stock returns and message me”.
          </div>
        </div>
        {actions}
      </div>
      <div className="panel-scroll">{body}</div>
    </div>
  );
}
