import React, { useState } from "react";
import { useChat } from "../lib/useChat";
import Transcript from "../components/Transcript";
import ToolTrace from "../components/ToolTrace";
import { AppStatus } from "../lib/types";

export default function ChatView({ status }: { status: AppStatus | null }) {
  const chat = useChat();
  const [draft, setDraft] = useState("");
  const notReady = status !== null && !status.ready;

  const submit = () => {
    if (!draft.trim() || chat.sending) return;
    chat.send(draft);
    setDraft("");
  };

  const onKey = (e: React.KeyboardEvent<HTMLTextAreaElement>) => {
    if (e.key === "Enter" && !e.shiftKey) {
      e.preventDefault();
      submit();
    }
  };

  const empty = (
    <div className="empty-state">
      <div className="big">💬</div>
      <h3>Start a conversation</h3>
      <p>Chat with SuperAI. It can use tools, search memory and the web, and stream its reasoning live.</p>
      {notReady && (
        <div className="empty-hint">⚙️ Backend not ready — configure your LLM in Settings to begin.</div>
      )}
    </div>
  );

  return (
    <div className="view">
      <div className="chat-layout">
        <div className="chat-main">
          <Transcript className="transcript" messages={chat.messages} empty={empty} />
          {chat.error && (
            <div style={{ padding: "0 24px", color: "var(--red)", fontSize: 12 }}>⚠ {chat.error}</div>
          )}
          <div className="composer">
            <div className="composer-box">
              <textarea
                value={draft}
                onChange={(e) => setDraft(e.target.value)}
                onKeyDown={onKey}
                placeholder={notReady ? "Configure LLM in Settings first…" : "Message SuperAI…  (Enter to send, Shift+Enter for newline)"}
                rows={1}
                disabled={chat.sending}
              />
              <button className="send-btn" onClick={submit} disabled={chat.sending || !draft.trim()} title="Send">
                {chat.sending ? <span className="spinner" /> : "↑"}
              </button>
            </div>
          </div>
        </div>
        <ToolTrace trace={chat.trace} />
      </div>
    </div>
  );
}
