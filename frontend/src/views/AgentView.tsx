import React, { useEffect, useState } from "react";
import { useChat } from "../lib/useChat";
import { AppStatus, TraceItem } from "../lib/types";
import { copyText } from "../lib/format";
import { Deliverables, ReadWorkspaceFile } from "../../wailsjs/go/main/App";
import { agent } from "../../wailsjs/go/models";
import {
  Conversation,
  ConversationContent,
  ConversationEmptyState,
} from "@/components/ai-elements/conversation";
import { Message, MessageContent } from "@/components/ai-elements/message";
import { Response } from "@/components/ai-elements/response";
import { Actions, Action } from "@/components/ai-elements/actions";
import {
  Tool,
  ToolHeader,
  ToolContent,
  ToolInput,
  ToolOutput,
  ToolState,
} from "@/components/ai-elements/tool";
import { Button } from "@/components/ui/button";
import { BotIcon, CopyIcon, PlayIcon, Loader2Icon } from "lucide-react";

function fmtSize(n: number): string {
  if (n < 1024) return `${n} B`;
  if (n < 1024 * 1024) return `${(n / 1024).toFixed(1)} KB`;
  return `${(n / 1024 / 1024).toFixed(1)} MB`;
}

function iconFor(type: string, path: string): string {
  const t = (type || "").toLowerCase();
  const p = (path || "").toLowerCase();
  if (t.includes("image") || /\.(png|jpe?g|gif|svg|webp)$/.test(p)) return "🖼️";
  if (/\.(md|markdown)$/.test(p)) return "📝";
  if (/\.(json|ya?ml|toml)$/.test(p)) return "🧩";
  if (/\.(csv|xlsx?|tsv)$/.test(p)) return "📊";
  if (/\.(pdf)$/.test(p)) return "📕";
  if (/\.(html?|css|tsx?|jsx?|go|py)$/.test(p)) return "💻";
  return "📄";
}

function traceState(status: TraceItem["status"]): ToolState {
  if (status === "ok") return "output-available";
  if (status === "fail") return "output-error";
  return "input-available";
}

function TraceTool({ t }: { t: TraceItem }) {
  const state = traceState(t.status);
  const r = t.result;
  const errorText =
    state === "output-error"
      ? typeof r === "string"
        ? r
        : JSON.stringify(r?.error ?? r, null, 2)
      : undefined;
  return (
    <Tool className={t.inner ? "ml-3 border-dashed" : ""}>
      <ToolHeader type={(t.inner ? "↳ " : "") + t.tool} state={state} />
      <ToolContent>
        {t.args && Object.keys(t.args).length > 0 && <ToolInput input={t.args} />}
        {state !== "input-available" && (
          <ToolOutput
            errorText={errorText}
            output={
              errorText ? undefined : (
                <pre>
                  <code>
                    {typeof r === "string" ? r : JSON.stringify(r, null, 2)}
                  </code>
                </pre>
              )
            }
          />
        )}
      </ToolContent>
    </Tool>
  );
}

export default function AgentView({ status }: { status: AppStatus | null }) {
  const chat = useChat();
  const [task, setTask] = useState("");
  const [deliverables, setDeliverables] = useState<agent.Deliverable[]>([]);
  const [viewer, setViewer] = useState<{ path: string; content: string } | null>(null);
  const [viewerLoading, setViewerLoading] = useState(false);
  const notReady = status !== null && !status.ready;

  const refreshDeliverables = async () => {
    try {
      const d = await Deliverables();
      setDeliverables(d || []);
    } catch {
      /* ignore */
    }
  };

  useEffect(() => {
    chat.onDone(() => {
      refreshDeliverables();
    });
    refreshDeliverables();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  const run = () => {
    if (!task.trim() || chat.sending) return;
    chat.send(task);
  };

  const openFile = async (path: string) => {
    setViewer({ path, content: "" });
    setViewerLoading(true);
    try {
      const content = await ReadWorkspaceFile(path);
      setViewer({ path, content });
    } catch (e: any) {
      setViewer({ path, content: `Failed to read file:\n${String(e?.message || e)}` });
    } finally {
      setViewerLoading(false);
    }
  };

  return (
    <div className="view">
      <div className="view-header">
        <div className="view-title">Agent</div>
        <div className="view-desc">Run an autonomous task. Outputs land in your workspace as deliverables.</div>
      </div>
      <div className="agent-layout">
        <div className="agent-main">
          <div className="task-input-wrap">
            <textarea
              className="task-textarea"
              placeholder={notReady ? "Configure LLM in Settings first…" : "Describe the task… e.g. 'Research the top 3 Wails alternatives and write a comparison to report.md'"}
              value={task}
              onChange={(e) => setTask(e.target.value)}
              disabled={chat.sending}
            />
            <div style={{ display: "flex", gap: 10, alignItems: "center" }}>
              <Button onClick={run} disabled={chat.sending || !task.trim()}>
                {chat.sending ? (
                  <>
                    <Loader2Icon className="size-4 animate-spin" /> Running…
                  </>
                ) : (
                  <>
                    <PlayIcon className="size-4" /> Run task
                  </>
                )}
              </Button>
              {chat.error && <span style={{ color: "var(--red)", fontSize: 12 }}>⚠ {chat.error}</span>}
            </div>
          </div>

          <Conversation className="agent-transcript">
            {chat.messages.length === 0 ? (
              <ConversationEmptyState
                icon={<BotIcon className="size-10" />}
                title="Autonomous Agent"
                description="Give SuperAI a goal and it will plan, use tools, and produce deliverables in your workspace."
              />
            ) : (
              <ConversationContent autoScrollKey={chat.messages.map((m) => m.content).join("|")}>
                {chat.messages.map((m) => (
                  <div key={m.id}>
                    <Message from={m.role}>
                      <MessageContent variant="flat">
                        {m.role === "assistant" ? (
                          m.content ? (
                            <Response>{m.content}</Response>
                          ) : m.streaming ? (
                            <span className="msg-cursor" />
                          ) : (
                            "…"
                          )
                        ) : (
                          <span style={{ whiteSpace: "pre-wrap" }}>{m.content}</span>
                        )}
                      </MessageContent>
                    </Message>
                    {m.role === "assistant" && !m.streaming && m.content && (
                      <Actions className="mt-1 ml-1">
                        <Action label="Copy" tooltip="Copy message" onClick={() => copyText(m.content)}>
                          <CopyIcon className="size-3.5" />
                        </Action>
                      </Actions>
                    )}
                  </div>
                ))}
              </ConversationContent>
            )}
          </Conversation>
        </div>

        <div className="trace-panel">
          <div className="trace-head">
            <span>Tool Trace</span>
            <span style={{ color: "var(--text-3)", fontWeight: 400 }}>{chat.trace.length}</span>
          </div>
          <div className="trace-list" style={{ display: "flex", flexDirection: "column", gap: 8, padding: 8 }}>
            {chat.trace.length === 0 ? (
              <div className="trace-empty">
                No tool activity yet.
                <br />
                Tools run during a task appear here live.
              </div>
            ) : (
              chat.trace.map((t) => <TraceTool key={t.id} t={t} />)
            )}
          </div>
        </div>
      </div>

      <DeliverablesBar
        deliverables={deliverables}
        onOpen={openFile}
        onRefresh={refreshDeliverables}
      />

      {viewer && (
        <div className="modal-overlay" onClick={() => setViewer(null)}>
          <div className="modal" onClick={(e) => e.stopPropagation()}>
            <div className="modal-head">
              <span className="modal-title">{viewer.path}</span>
              <button className="modal-close" onClick={() => setViewer(null)}>×</button>
            </div>
            <div className="modal-body">
              {viewerLoading ? (
                <div className="loading-row"><span className="spinner" style={{ borderTopColor: "var(--accent)" }} /> Loading…</div>
              ) : (
                <pre>{viewer.content}</pre>
              )}
            </div>
          </div>
        </div>
      )}
    </div>
  );

  function DeliverablesBar({
    deliverables,
    onOpen,
    onRefresh,
  }: {
    deliverables: agent.Deliverable[];
    onOpen: (p: string) => void;
    onRefresh: () => void;
  }) {
    return (
      <div style={{ borderTop: "1px solid var(--border)", background: "var(--bg-1)", maxHeight: 200, display: "flex", flexDirection: "column" }}>
        <div className="trace-head">
          <span>Deliverables ({deliverables.length})</span>
          <button className="btn ghost sm" onClick={onRefresh}>↻ Refresh</button>
        </div>
        <div style={{ overflowY: "auto", padding: 10, display: "grid", gridTemplateColumns: "repeat(auto-fill, minmax(220px, 1fr))", gap: 6 }}>
          {deliverables.length === 0 ? (
            <div className="trace-empty">No deliverables yet. Run a task that writes files to the workspace.</div>
          ) : (
            deliverables.map((d) => (
              <button key={d.path} className="deliv-item" onClick={() => onOpen(d.path)}>
                <span className="deliv-icon">{iconFor(d.type, d.path)}</span>
                <span className="deliv-meta">
                  <div className="deliv-name">{d.path.split("/").pop() || d.path}</div>
                  <div className="deliv-sub">{fmtSize(d.size)}</div>
                </span>
              </button>
            ))
          )}
        </div>
      </div>
    );
  }
}
