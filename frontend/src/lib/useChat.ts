import { useCallback, useEffect, useRef, useState } from "react";
import { EventsOn } from "../../wailsjs/runtime";
import { SendChat } from "../../wailsjs/go/main/App";
import { ChatEvent, ChatMessage, ChatDone, ChatError, TraceItem } from "./types";

let sessionCounter = 0;
function makeId(): string {
  try {
    if (typeof crypto !== "undefined" && crypto.randomUUID) return crypto.randomUUID();
  } catch {
    /* ignore */
  }
  sessionCounter += 1;
  return `id-${Date.now()}-${sessionCounter}`;
}

interface UseChatResult {
  sessionId: string;
  messages: ChatMessage[];
  trace: TraceItem[];
  sending: boolean;
  lastEmotion: string;
  error: string;
  send: (text: string, imagePaths?: string[]) => Promise<void>;
  onDone: (cb: () => void) => void;
  reset: () => void;
  clear: () => void;
}

export function useChat(): UseChatResult {
  const sessionId = useRef<string>(makeId()).current;
  const [messages, setMessages] = useState<ChatMessage[]>([]);
  const [trace, setTrace] = useState<TraceItem[]>([]);
  const [sending, setSending] = useState(false);
  const [lastEmotion, setLastEmotion] = useState("");
  const [error, setError] = useState("");
  const doneCb = useRef<(() => void) | null>(null);
  const assistantId = useRef<string>("");

  const finishStreaming = useCallback((finalText?: string, emotion?: string) => {
    setMessages((prev) =>
      prev.map((m) =>
        m.id === assistantId.current
          ? {
              ...m,
              content: finalText && finalText.length ? finalText : m.content,
              emotion: emotion || m.emotion,
              streaming: false,
            }
          : m
      )
    );
    setSending(false);
    if (emotion) setLastEmotion(emotion);
  }, []);

  useEffect(() => {
    // Wails runtime is only present inside the desktop webview; guard so the
    // SPA can also render in a plain browser (dev inspection) without crashing.
    if (typeof window === "undefined" || !(window as any).runtime) {
      return;
    }
    const offEvent = EventsOn("chat:event", (payload: ChatEvent) => {
      if (!payload) return;
      switch (payload.type) {
        case "partial": {
          if (!payload.content) return;
          setMessages((prev) =>
            prev.map((m) =>
              m.id === assistantId.current
                ? { ...m, content: m.content + payload.content }
                : m
            )
          );
          break;
        }
        case "tool_call": {
          const inner = payload.debugType === "ptc_inner";
          setTrace((prev) => [
            ...prev,
            {
              id: makeId(),
              tool: payload.tool || "tool",
              args: payload.args || {},
              inner,
              status: "running",
            },
          ]);
          break;
        }
        case "tool_result": {
          setTrace((prev) => {
            // mark the most recent running matching tool as resolved
            const next = [...prev];
            for (let i = next.length - 1; i >= 0; i--) {
              if (next[i].status === "running" && (!payload.tool || next[i].tool === payload.tool)) {
                const r = payload.result;
                const failed =
                  r && typeof r === "object" && (r.ok === false || r.error || r.success === false);
                next[i] = { ...next[i], status: failed ? "fail" : "ok", result: r };
                break;
              }
            }
            return next;
          });
          break;
        }
        case "workflow_error": {
          setError(payload.content || "workflow error");
          finishStreaming();
          break;
        }
        case "workflow_blocked": {
          finishStreaming(payload.content || undefined);
          break;
        }
        default:
          break;
      }
    });

    const offDone = EventsOn("chat:done", (payload: ChatDone) => {
      finishStreaming(payload?.final, payload?.emotion);
      setTrace((prev) =>
        prev.map((t) => (t.status === "running" ? { ...t, status: "ok" } : t))
      );
      if (doneCb.current) doneCb.current();
    });

    const offErr = EventsOn("chat:error", (payload: ChatError) => {
      setError(payload?.error || "unknown error");
      finishStreaming();
    });

    return () => {
      offEvent();
      offDone();
      offErr();
    };
  }, [finishStreaming]);

  const send = useCallback(
    async (text: string, imagePaths: string[] = []) => {
      const trimmed = text.trim();
      if ((!trimmed && imagePaths.length === 0) || sending) return;
      setError("");
      setTrace([]);
      const userMsg: ChatMessage = { id: makeId(), role: "user", content: trimmed };
      const aId = makeId();
      assistantId.current = aId;
      const assistantMsg: ChatMessage = {
        id: aId,
        role: "assistant",
        content: "",
        streaming: true,
      };
      setMessages((prev) => [...prev, userMsg, assistantMsg]);
      setSending(true);
      try {
        await SendChat(sessionId, trimmed, imagePaths);
      } catch (e: any) {
        setError(String(e?.message || e));
        finishStreaming();
      }
    },
    [sending, sessionId, finishStreaming]
  );

  const onDone = useCallback((cb: () => void) => {
    doneCb.current = cb;
  }, []);

  const reset = useCallback(() => {
    setMessages([]);
    setTrace([]);
    setError("");
    setLastEmotion("");
  }, []);

  return { sessionId, messages, trace, sending, lastEmotion, error, send, onDone, reset, clear: reset };
}
