import React, { useCallback, useEffect, useRef, useState } from "react";
import {
  DeleteScheduledPrompt,
  RunScheduledPromptNow,
  SchedulePrompt,
  ScheduledPrompts,
  SetScheduledPromptEnabled,
} from "../../wailsjs/go/main/App";
import { agent } from "../../wailsjs/go/models";
import { AppStatus } from "../lib/types";
import { DEFAULT_WHEN, When, describeCron, previewCron, whenToCron } from "../lib/cron";
import { firstLine, formatStamp, fromNow, parseTime } from "../lib/format";
import { ScheduleRunLog } from "../lib/useScheduleRuns";
import WhenPicker from "../components/WhenPicker";
import { ScheduleRunList } from "../components/ScheduleRuns";

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
}: {
  status: AppStatus | null;
  log: ScheduleRunLog;
  onOpenConversation: (session: string) => void;
}) {
  const [list, setList] = useState<agent.ScheduledPrompt[]>([]);
  const [loading, setLoading] = useState(true);
  const [note, setNote] = useState<Note>(null);
  const [composing, setComposing] = useState(false);
  const [prompt, setPrompt] = useState("");
  const [name, setName] = useState("");
  const [session, setSession] = useState("");
  const [when, setWhen] = useState<When>(DEFAULT_WHEN);
  const [saving, setSaving] = useState(false);
  const [busy, setBusy] = useState("");
  const [confirmDelete, setConfirmDelete] = useState("");
  const [fresh, setFresh] = useState("");

  const load = useCallback(async () => {
    setLoading(true);
    try {
      const res = await ScheduledPrompts();
      setList(Array.isArray(res) ? res : []);
    } catch (e: any) {
      setNote({ kind: "err", text: String(e?.message || e) });
    } finally {
      setLoading(false);
    }
  }, []);

  useEffect(() => {
    load();
  }, [load]);

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

  const cron = whenToCron(when);
  // An expression this parser calls invalid is one the backend also refuses, so
  // saying so here saves a round trip that would answer with a robfig error
  // message ("end of range (70) above maximum (59)") instead of the field's own.
  // Only "invalid" blocks: "unknown" means we declined to judge, not that it is
  // wrong, and the backend understands more dialects than this does.
  const cronWhy = cron === "" ? null : previewCron(cron, 1);
  const cronBad = cronWhy?.kind === "invalid" ? cronWhy.why : "";
  const canCreate = prompt.trim() !== "" && cron !== "" && cronBad === "" && !saving;

  const create = async () => {
    setSaving(true);
    setNote(null);
    const known = new Set(list.map((s) => s.id));
    try {
      const res = await SchedulePrompt(prompt.trim(), cron, name.trim(), session.trim());
      if (res !== "ok") {
        setNote({ kind: "err", text: res });
        return;
      }
      const next = await ScheduledPrompts();
      const rows = Array.isArray(next) ? next : [];
      setList(rows);
      // Confirming with the backend's own next_run rather than the local preview:
      // the preview is a guess about the expression, this is the answer.
      const created = rows.find((s) => !known.has(s.id));
      const at = created ? parseTime(created.next_run) : null;
      setFresh(created?.id || "");
      setNote({
        kind: "ok",
        text: at
          ? `Scheduled. First run ${formatStamp(at)} — ${fromNow(at)}.`
          : "Scheduled.",
      });
      setComposing(false);
      setPrompt("");
      setName("");
      setSession("");
      setWhen(DEFAULT_WHEN);
    } catch (e: any) {
      setNote({ kind: "err", text: String(e?.message || e) });
    } finally {
      setSaving(false);
    }
  };

  const act = async (id: string, label: string, call: () => Promise<string>) => {
    setBusy(id);
    setNote(null);
    setFresh("");
    setConfirmDelete("");
    try {
      const res = await call();
      setNote(res === "ok" ? { kind: "ok", text: `${label} done.` } : { kind: "err", text: res });
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
        <div className="vh-actions">
          <button
            className="btn ghost sm"
            onClick={() => {
              setFresh("");
              setComposing((v) => !v);
            }}
          >
            {composing ? "Close" : "+ New schedule"}
          </button>
          <button className="btn ghost sm" onClick={load} disabled={loading}>
            {loading ? (
              <>
                <span className="spinner" style={{ borderTopColor: "var(--text-1)" }} /> Loading…
              </>
            ) : (
              "↻ Refresh"
            )}
          </button>
        </div>
      </div>

      <div className="panel-scroll">
        {composing && (
          <div className="card" style={{ marginBottom: 14 }}>
            <div className="card-title">New schedule</div>
            <div className="card-desc">
              Say what SuperAI should do and when. It runs on its own and the answer lands in a
              conversation you can open afterwards.
            </div>
            <div className="field">
              <label>What should SuperAI do?</label>
              <textarea
                className="input"
                rows={3}
                autoFocus
                value={prompt}
                placeholder="Work out my stock returns and message me"
                onChange={(e) => setPrompt(e.target.value)}
              />
              <span className="hint">
                Write it as an instruction, exactly as you would type it into chat.
              </span>
            </div>

            <WhenPicker when={when} onChange={setWhen} />

            <div className="row2">
              <div className="field">
                <label>Name</label>
                <input
                  className="input"
                  value={name}
                  autoComplete="off"
                  placeholder="Morning stock report"
                  onChange={(e) => setName(e.target.value)}
                />
                <span className="hint">Optional. Shown in this list; defaults to the prompt.</span>
              </div>
              <div className="field">
                <label>Conversation</label>
                <input
                  className="input"
                  value={session}
                  autoComplete="off"
                  placeholder="scheduled"
                  onChange={(e) => setSession(e.target.value)}
                />
                <span className="hint">
                  Optional. Runs append to this conversation; blank shares one called “scheduled”.
                </span>
              </div>
            </div>

            <div style={{ display: "flex", alignItems: "center", gap: 12 }}>
              <button className="btn" onClick={create} disabled={!canCreate}>
                {saving ? (
                  <>
                    <span className="spinner" /> Scheduling…
                  </>
                ) : (
                  "Create schedule"
                )}
              </button>
              {prompt.trim() === "" ? (
                <span className="save-note">Describe what it should do first.</span>
              ) : cronBad !== "" ? (
                <span className="save-note err">{cronBad}</span>
              ) : null}
            </div>
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
              const running = busy === s.id;
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
                    <span className="sched-actions">
                      {/* Run now is the only way to find out whether a schedule
                          does what was meant without waiting for the hour, and
                          the call does not return until the turn is over. */}
                      <button
                        className="btn ghost sm"
                        disabled={running}
                        title="Run this prompt immediately"
                        onClick={() => act(s.id, "Run", () => RunScheduledPromptNow(s.id))}
                      >
                        {running ? (
                          <>
                            <span className="spinner" style={{ borderTopColor: "var(--text-1)" }} />{" "}
                            Running…
                          </>
                        ) : (
                          "Run now"
                        )}
                      </button>
                      <button
                        className="btn ghost sm"
                        disabled={running}
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
                        disabled={running}
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
      </div>
    </div>
  );
}
