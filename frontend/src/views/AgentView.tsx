import React, { useEffect, useState } from "react";
import { useChat } from "../lib/useChat";
import Transcript from "../components/Transcript";
import ToolTrace from "../components/ToolTrace";
import { AppStatus } from "../lib/types";
import { Deliverables, ReadWorkspaceFile } from "../../wailsjs/go/main/App";
import { agent } from "../../wailsjs/go/models";

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

  const empty = (
    <div className="empty-state">
      <div className="big">🤖</div>
      <h3>Autonomous Agent</h3>
      <p>Give SuperAI a goal and it will plan, use tools, and produce deliverables in your workspace.</p>
    </div>
  );

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
              <button className="btn" onClick={run} disabled={chat.sending || !task.trim()}>
                {chat.sending ? <><span className="spinner" /> Running…</> : <>▶ Run task</>}
              </button>
              {chat.error && <span style={{ color: "var(--red)", fontSize: 12 }}>⚠ {chat.error}</span>}
            </div>
          </div>
          <Transcript className="agent-transcript" messages={chat.messages} empty={empty} />
        </div>

        <div style={{ display: "flex", flexDirection: "column", minHeight: 0 }}>
          <div style={{ flex: 1, minHeight: 0, display: "flex" }}>
            <ToolTrace trace={chat.trace} />
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
