import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { EventsOn } from "../../wailsjs/runtime";
import { CancelChat, ChatHistory, SendChat } from "../../wailsjs/go/main/App";
import {
  AskSummary,
  ChatCancelled,
  ChatDone,
  ChatError,
  ChatEvent,
  ChatMessage,
  ProgressStep,
  TraceItem,
} from "./types";
import { visibleAnswer } from "./format";

let sessionCounter = 0;
function makeId(): string {
  try {
    if (typeof crypto !== "undefined" && crypto.randomUUID)
      return crypto.randomUUID();
  } catch {
    /* ignore */
  }
  sessionCounter += 1;
  return `id-${Date.now()}-${sessionCounter}`;
}

/** A payload waiting for its request id to be bound to an ask. */
type Queued =
  | { kind: "event"; payload: ChatEvent }
  | { kind: "done"; payload: ChatDone }
  | { kind: "error"; payload: ChatError }
  | { kind: "cancelled"; payload: ChatCancelled };

/**
 * Progress text is written for a human but arrives with the model's markdown
 * emphasis still on it ("**Designing synchronous JS tool call**"), and a status
 * line is a single line.
 */
function cleanProgress(raw: string): string {
  return (raw || "")
    .replace(/\s+/g, " ")
    .replace(/^[*_`\s]+/, "")
    .replace(/[*_`\s]+$/, "")
    .trim();
}

function summarizeAsks(messages: ChatMessage[]): AskSummary[] {
  const out: AskSummary[] = [];
  messages.forEach((m, i) => {
    if (m.role !== "assistant" || !m.startedAt) return;
    const prev = messages[i - 1];
    out.push({
      id: m.id,
      prompt: prev && prev.role === "user" ? prev.content : "",
      status: m.streaming
        ? "streaming"
        : m.cancelled
          ? "cancelled"
          : m.error
            ? "error"
            : "done",
    });
  });
  return out;
}

interface UseChatResult {
  sessionId: string;
  messages: ChatMessage[];
  trace: TraceItem[];
  /** The asks this transcript owns, oldest first. */
  asks: AskSummary[];
  /** True while at least one ask is still streaming. Does not block sending. */
  sending: boolean;
  lastEmotion: string;
  send: (text: string, imagePaths?: string[]) => Promise<void>;
  /**
   * Stop a running ask, or every running ask when called with no id. The turn
   * is cancelled on the backend; whatever it already streamed stays in the
   * bubble, and the composer never stops accepting the next question.
   */
  cancel: (askId?: string) => Promise<void>;
  onDone: (cb: () => void) => void;
  reset: () => void;
  clear: () => void;
  /** Restore a past conversation into the transcript and continue it. */
  loadSession: (id: string) => Promise<void>;
  /** Start a fresh conversation, leaving the previous one in history. */
  newSession: () => void;
}

export function useChat(): UseChatResult {
  // The session is state, not a fixed ref: picking a conversation out of
  // history means continuing that session id, not starting a new one.
  const [sessionId, setSessionId] = useState<string>(makeId);
  const [messages, setMessages] = useState<ChatMessage[]>([]);
  const [trace, setTrace] = useState<TraceItem[]>([]);
  const [lastEmotion, setLastEmotion] = useState("");
  const doneCb = useRef<(() => void) | null>(null);

  // An ask is identified by its assistant message id. Nothing about a stream
  // lives in a single "current" slot, so a second question asked while the
  // first is still answering gets its own bubble, trace and progress.
  const bound = useRef(new Map<string, string>()); // requestId -> ask id
  const request = useRef(new Map<string, string>()); // ask id -> requestId, the handle a stop needs
  const queue = useRef(new Map<string, Queued[]>()); // requestId -> events seen before the bind
  const unbound = useRef(0); // sends whose SendChat call has not returned an id yet
  const attached = useRef(new Set<string>()); // asks belonging to the transcript on screen
  const running = useRef(new Set<string>()); // asks that have not finished
  // Stopped asks. A cancelled turn keeps emitting for a moment — the agent loop
  // winds down with a tombstone and a workflow_cancelled on its way out — and
  // none of that must repaint a deliberate stop as a failure.
  const stopped = useRef(new Set<string>());
  // Stops asked for before SendChat returned the id they need. Replayed at the
  // bind, so a stop pressed in the first instant is not silently lost.
  const wantStop = useRef(new Set<string>());
  // Raw stream per ask. The bubble shows visibleAnswer() of this rather than the
  // deltas themselves, and stripping needs the whole text: a <code> block is cut
  // across deltas wherever the provider happens to flush.
  const streamed = useRef(new Map<string, string>());

  const patchMessage = useCallback(
    (askId: string, patch: (m: ChatMessage) => ChatMessage) => {
      setMessages((prev) => prev.map((m) => (m.id === askId ? patch(m) : m)));
    },
    [],
  );

  const pushProgress = useCallback(
    (askId: string, step: Omit<ProgressStep, "id">) => {
      patchMessage(askId, (m) => {
        const steps = m.progress || [];
        const last = steps[steps.length - 1];
        // Agents repeat themselves ("Thinking..." twice in a row); the repeat
        // carries no information, but the same line later in the run does.
        if (last && last.kind === step.kind && last.text === step.text)
          return m;
        return { ...m, progress: [...steps, { id: makeId(), ...step }] };
      });
    },
    [patchMessage],
  );

  const finishAsk = useCallback(
    (
      askId: string,
      opts: {
        final?: string;
        emotion?: string;
        error?: string;
        cancelled?: boolean;
      },
    ) => {
      running.current.delete(askId);
      streamed.current.delete(askId);
      wantStop.current.delete(askId);
      if (opts.cancelled) stopped.current.add(askId);
      patchMessage(askId, (m) => ({
        ...m,
        content: opts.final && opts.final.length ? opts.final : m.content,
        emotion: opts.emotion || m.emotion,
        error: opts.error || m.error,
        cancelled: opts.cancelled || m.cancelled,
        stopping: false,
        streaming: false,
        finishedAt: Date.now(),
      }));
      if (opts.emotion) setLastEmotion(opts.emotion);
      // A tool still marked running when the turn ends never reports back, so
      // resolve it here rather than leaving a clock spinning forever.
      setTrace((prev) =>
        prev.map((t) =>
          t.askId === askId && t.status === "running"
            ? { ...t, status: opts.error ? "fail" : "ok" }
            : t,
        ),
      );
    },
    [patchMessage],
  );

  const applyEvent = useCallback(
    (askId: string, ev: ChatEvent) => {
      switch (ev.type) {
        case "partial": {
          if (!ev.content) return;
          const raw = (streamed.current.get(askId) || "") + ev.content;
          streamed.current.set(askId, raw);
          const shown = visibleAnswer(raw);
          patchMessage(askId, (m) =>
            m.content === shown ? m : { ...m, content: shown },
          );
          break;
        }
        case "thinking":
        case "state_update": {
          const text = cleanProgress(ev.content);
          if (!text) return;
          pushProgress(askId, {
            kind: ev.type === "thinking" ? "thinking" : "state",
            text,
          });
          break;
        }
        case "tool_call": {
          const inner = ev.debugType === "ptc_inner";
          const tool = ev.tool || "tool";
          setTrace((prev) => [
            ...prev,
            {
              id: makeId(),
              askId,
              tool,
              args: ev.args || {},
              inner,
              status: "running",
            },
          ]);
          // Inner calls are the ones the model's own code makes; there can be
          // dozens of them and the trace panel already shows them nested, so
          // only top-level tools become a progress line.
          if (!inner)
            pushProgress(askId, {
              kind: "tool",
              text: `Calling ${tool}`,
              tool,
            });
          break;
        }
        case "tool_result": {
          setTrace((prev) => {
            // Mark this ask's most recent running match as resolved. Scoping to
            // the ask matters: a sibling turn may be running the same tool.
            const next = [...prev];
            for (let i = next.length - 1; i >= 0; i--) {
              const t = next[i];
              if (t.askId !== askId || t.status !== "running") continue;
              if (ev.tool && t.tool !== ev.tool) continue;
              const r = ev.result;
              const failed =
                r &&
                typeof r === "object" &&
                (r.ok === false || r.error || r.success === false);
              next[i] = { ...t, status: failed ? "fail" : "ok", result: r };
              break;
            }
            return next;
          });
          break;
        }
        case "workflow_error": {
          finishAsk(askId, { error: ev.content || "workflow error" });
          break;
        }
        case "workflow_cancelled": {
          // The run stopped before it finished — the stop button, or the
          // turn's own deadline. An outcome, not a failure: keep whatever was
          // streamed and settle the ask as cancelled. Normally the stop path
          // has already done this and dispatch drops the event; this is the
          // case where nobody on this side asked for it.
          finishAsk(askId, { cancelled: true });
          break;
        }
        case "workflow_blocked": {
          finishAsk(askId, { final: ev.content || undefined });
          break;
        }
        default:
          break;
      }
    },
    [finishAsk, patchMessage, pushProgress],
  );

  const dispatch = useCallback(
    (askId: string, item: Queued) => {
      // A stopped ask is over. The backend is still winding the turn down and
      // its parting events — a tombstone, a last partial, workflow_cancelled,
      // and finally chat:cancelled itself — would otherwise reopen an ask the
      // user has already finished with.
      if (stopped.current.has(askId)) return;
      switch (item.kind) {
        case "event":
          applyEvent(askId, item.payload);
          break;
        case "done":
          finishAsk(askId, {
            final: item.payload?.final,
            emotion: item.payload?.emotion,
          });
          if (doneCb.current) doneCb.current();
          break;
        case "error":
          finishAsk(askId, { error: item.payload?.error || "unknown error" });
          break;
        case "cancelled":
          finishAsk(askId, {
            final: item.payload?.final || undefined,
            cancelled: true,
          });
          break;
      }
    },
    [applyEvent, finishAsk],
  );

  const route = useCallback(
    (requestId: string, item: Queued) => {
      let askId = requestId ? bound.current.get(requestId) : undefined;
      // An untagged payload can still be placed while exactly one ask is
      // running. That keeps the transcript working against a backend build
      // that does not tag some event type yet, and is unambiguous by
      // construction — with two asks in flight it is dropped instead.
      if (!requestId && running.current.size === 1) {
        askId = running.current.values().next().value;
      }
      if (askId) {
        if (attached.current.has(askId)) dispatch(askId, item);
        return;
      }
      // The backend emits from a goroutine while the Wails call returns
      // separately, so events routinely arrive before we know their id. Hold
      // them until the bind, which replays them in arrival order. If no send is
      // still waiting for an id, nothing can ever claim this payload (the
      // "backend not ready" error is tagged with an id SendChat never returns),
      // so dropping it is the only way it does not leak.
      if (!requestId || unbound.current === 0) return;
      const q = queue.current.get(requestId);
      if (q) q.push(item);
      else queue.current.set(requestId, [item]);
    },
    [dispatch],
  );

  useEffect(() => {
    // Wails runtime is only present inside the desktop webview; guard so the
    // SPA can also render in a plain browser (dev inspection) without crashing.
    if (typeof window === "undefined" || !(window as any).runtime) {
      return;
    }
    const offEvent = EventsOn("chat:event", (payload: ChatEvent) => {
      if (!payload) return;
      route(payload.requestId || "", { kind: "event", payload });
    });
    const offDone = EventsOn("chat:done", (payload: ChatDone) => {
      route(payload?.requestId || "", { kind: "done", payload });
    });
    const offErr = EventsOn("chat:error", (payload: ChatError) => {
      route(payload?.requestId || "", { kind: "error", payload });
    });
    const offCancel = EventsOn("chat:cancelled", (payload: ChatCancelled) => {
      route(payload?.requestId || "", { kind: "cancelled", payload });
    });
    return () => {
      offEvent();
      offDone();
      offErr();
      offCancel();
    };
  }, [route]);

  /**
   * Tie a request id to the ask that started it and replay whatever arrived in
   * the meantime. Must stay synchronous: an event delivered between the bind
   * and the replay would otherwise overtake the buffered ones.
   */
  const bindAsk = useCallback(
    (askId: string, requestId: string) => {
      unbound.current -= 1;
      const held = queue.current.get(requestId);
      queue.current.delete(requestId);
      if (attached.current.has(askId)) {
        bound.current.set(requestId, askId);
        request.current.set(askId, requestId);
        held?.forEach((item) => dispatch(askId, item));
      }
      // Anything still held once every send knows its id is unclaimable.
      if (unbound.current === 0) queue.current.clear();
    },
    [dispatch],
  );

  /**
   * Stop the turn behind a request id and settle the ask as cancelled once the
   * backend confirms it. A refusal ("already finished") is left alone: the real
   * terminal event is on its way and it, not this, decides how the ask ended.
   */
  const stopRequest = useCallback(
    async (askId: string, requestId: string) => {
      const clear = () =>
        patchMessage(askId, (m) => (m.stopping ? { ...m, stopping: false } : m));
      patchMessage(askId, (m) => (m.stopping ? m : { ...m, stopping: true }));
      try {
        const res = await CancelChat(requestId);
        if (res === "ok" && running.current.has(askId)) {
          finishAsk(askId, { cancelled: true });
          return;
        }
      } catch {
        /* the ask is ending one way or another; its own event will say how */
      }
      clear();
    },
    [finishAsk, patchMessage],
  );

  const cancel = useCallback(
    async (askId?: string) => {
      const targets = askId ? [askId] : Array.from(running.current);
      await Promise.all(
        targets.map((id) => {
          if (!running.current.has(id)) return Promise.resolve();
          const requestId = request.current.get(id);
          if (requestId) return stopRequest(id, requestId);
          // SendChat has not answered with an id yet. Remember the stop and
          // let the bind carry it through, rather than dropping it and leaving
          // a turn running that the user has already told us to abandon.
          wantStop.current.add(id);
          patchMessage(id, (m) => (m.stopping ? m : { ...m, stopping: true }));
          return Promise.resolve();
        }),
      );
    },
    [patchMessage, stopRequest],
  );

  const send = useCallback(
    async (text: string, imagePaths: string[] = []) => {
      const trimmed = text.trim();
      if (!trimmed && imagePaths.length === 0) return;
      const askId = makeId();
      const userMsg: ChatMessage = {
        id: makeId(),
        role: "user",
        content: trimmed,
      };
      const assistantMsg: ChatMessage = {
        id: askId,
        role: "assistant",
        content: "",
        streaming: true,
        progress: [],
        startedAt: Date.now(),
      };
      attached.current.add(askId);
      running.current.add(askId);
      unbound.current += 1;
      // A new ask retires the tool activity of asks that have already finished,
      // which keeps the panel about what is happening now — but never touches a
      // sibling that is still running.
      setTrace((prev) => prev.filter((t) => running.current.has(t.askId)));
      setMessages((prev) => [...prev, userMsg, assistantMsg]);
      try {
        const requestId = await SendChat(sessionId, trimmed, imagePaths);
        if (requestId) {
          bindAsk(askId, requestId);
          // A stop pressed before the id came back is honoured now that it can
          // name the run it meant.
          if (wantStop.current.delete(askId)) void stopRequest(askId, requestId);
          return;
        }
        // Nothing was started. The backend also emits a tagged error, but it is
        // tagged with an id we were never given, so report it from here.
        unbound.current -= 1;
        if (unbound.current === 0) queue.current.clear();
        if (attached.current.has(askId)) {
          finishAsk(askId, {
            error: "backend not ready — configure your LLM in Settings",
          });
        }
      } catch (e: any) {
        unbound.current -= 1;
        if (unbound.current === 0) queue.current.clear();
        if (attached.current.has(askId)) {
          finishAsk(askId, { error: String(e?.message || e) });
        }
      }
    },
    [sessionId, bindAsk, finishAsk, stopRequest],
  );

  const onDone = useCallback((cb: () => void) => {
    doneCb.current = cb;
  }, []);

  /**
   * Let go of every ask on screen without stopping it. Leaving a conversation
   * is not the same act as pressing stop: the turn keeps running and its answer
   * still lands in that conversation's history — it simply stops writing into a
   * transcript it is no longer part of. Use cancel() to actually end one.
   */
  const detachAll = useCallback(() => {
    attached.current.clear();
    running.current.clear();
    bound.current.clear();
    request.current.clear();
    stopped.current.clear();
    wantStop.current.clear();
    queue.current.clear();
  }, []);

  const reset = useCallback(() => {
    detachAll();
    setMessages([]);
    setTrace([]);
    setLastEmotion("");
  }, [detachAll]);

  const loadSession = useCallback(
    async (id: string) => {
      const turns = await ChatHistory(id);
      detachAll();
      setMessages(
        (turns || []).map((t) => ({
          id: makeId(),
          role: t.role as ChatMessage["role"],
          content: t.content,
          kind: t.kind === "context" ? ("context" as const) : undefined,
          emotion: t.emotion || undefined,
          // The working, recovered from the scripts the transcript hid, so a
          // reopened conversation still shows what the agent did and not just
          // what it concluded. Same progress block as a live turn, already
          // collapsed — the run is over.
          progress: (t.steps || []).map((text) => ({
            id: makeId(),
            kind: "tool" as const,
            text,
          })),
        })),
      );
      setTrace([]);
      setSessionId(id);
    },
    [detachAll],
  );

  const newSession = useCallback(() => {
    setSessionId(makeId());
    reset();
  }, [reset]);

  const sending = messages.some((m) => m.streaming);
  // Keyed on a signature rather than the array so the trace panel is not
  // re-rendered by every streamed token; the prompts it reads never change
  // after their message is appended.
  const askSignature = messages
    .map((m) =>
      m.startedAt
        ? `${m.id}:${
            m.streaming ? "s" : m.cancelled ? "c" : m.error ? "e" : "d"
          }`
        : "",
    )
    .join("|");
  // eslint-disable-next-line react-hooks/exhaustive-deps
  const asks = useMemo(() => summarizeAsks(messages), [askSignature]);

  return {
    sessionId,
    messages,
    trace,
    asks,
    sending,
    lastEmotion,
    send,
    cancel,
    onDone,
    reset,
    clear: reset,
    loadSession,
    newSession,
  };
}
