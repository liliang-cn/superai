import React, { useEffect, useState } from "react";
import { useChat } from "../lib/useChat";
import { AppStatus } from "../lib/types";
import { copyText } from "../lib/format";
import { OnFileDrop, OnFileDropOff } from "../../wailsjs/runtime";
import { ImportFiles, PickFiles } from "../../wailsjs/go/main/App";
import {
  Conversation,
  ConversationContent,
  ConversationEmptyState,
} from "@/components/ai-elements/conversation";
import { Message, MessageContent } from "@/components/ai-elements/message";
import { Response } from "@/components/ai-elements/response";
import { Actions, Action } from "@/components/ai-elements/actions";
import {
  PromptInput,
  PromptInputTextarea,
  PromptInputToolbar,
  PromptInputTools,
  PromptInputSubmit,
} from "@/components/ai-elements/prompt-input";
import { Button } from "@/components/ui/button";
import { CopyIcon, MessageSquareIcon, Trash2Icon, PaperclipIcon, XIcon, FileIcon } from "lucide-react";
import TracePanel from "../components/TracePanel";

export default function ChatView({ status }: { status: AppStatus | null }) {
  const chat = useChat();
  const notReady = status !== null && !status.ready;
  const messages = chat.messages;
  const [attachments, setAttachments] = useState<string[]>([]);
  const [importing, setImporting] = useState(false);

  const addPaths = (rels: string[] | null) => {
    if (!rels || rels.length === 0) return;
    setAttachments((prev) => Array.from(new Set([...prev, ...rels])));
  };

  // Native OS file drop -> copy into the agent workspace -> attach the path.
  useEffect(() => {
    OnFileDrop((_x, _y, paths) => {
      if (!paths || paths.length === 0) return;
      setImporting(true);
      ImportFiles(paths)
        .then((rels) => addPaths(rels))
        .catch(() => {})
        .finally(() => setImporting(false));
    }, true);
    return () => OnFileDropOff();
  }, []);

  const pickFiles = () => {
    setImporting(true);
    PickFiles()
      .then((rels) => addPaths(rels))
      .catch(() => {})
      .finally(() => setImporting(false));
  };

  const removeAttachment = (p: string) =>
    setAttachments((prev) => prev.filter((x) => x !== p));

  const onSubmit = (msg: { text: string }, e: React.FormEvent<HTMLFormElement>) => {
    const text = msg.text.trim();
    if (chat.sending) return;
    if (!text && attachments.length === 0) return;
    let payload = text;
    if (attachments.length > 0) {
      const list = attachments.map((p) => `- ${p}`).join("\n");
      const instruction = text || "读取这些文件并总结要点。";
      payload = `[附件文件，可用 read_document 工具读取]\n${list}\n\n${instruction}`;
      setAttachments([]);
    }
    chat.send(payload);
    e.currentTarget.reset();
  };

  return (
    <div className="view">
      <div className="chat-layout">
        <div className="chat-main" style={{ ["--wails-drop-target" as any]: "drop" }}>
          <Conversation className="transcript">
            {messages.length === 0 ? (
              <ConversationEmptyState
                icon={<MessageSquareIcon className="size-10" />}
                title="Start a conversation"
                description="Chat with SuperAI. It can use tools, search memory and the web, and stream its reasoning live."
              >
                {notReady && (
                  <div className="empty-hint">
                    ⚙️ Backend not ready — configure your LLM in Settings to begin.
                  </div>
                )}
              </ConversationEmptyState>
            ) : (
              <ConversationContent autoScrollKey={messages.map((m) => m.content).join("|")}>
                {messages.map((m) => (
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
                        <Action
                          label="Copy"
                          tooltip="Copy message"
                          onClick={() => copyText(m.content)}
                        >
                          <CopyIcon className="size-3.5" />
                        </Action>
                      </Actions>
                    )}
                    {m.emotion && !m.streaming && (
                      <div className="emotion-chip">🎭 {m.emotion}</div>
                    )}
                  </div>
                ))}
              </ConversationContent>
            )}
          </Conversation>

          {chat.error && (
            <div style={{ padding: "0 24px", color: "var(--red)", fontSize: 12 }}>
              ⚠ {chat.error}
            </div>
          )}

          <div className="composer">
            {attachments.length > 0 && (
              <div style={{ display: "flex", flexWrap: "wrap", gap: 6, padding: "0 2px 8px" }}>
                {attachments.map((p) => (
                  <span
                    key={p}
                    title={p}
                    style={{
                      display: "inline-flex", alignItems: "center", gap: 6,
                      fontSize: 12, padding: "3px 8px", borderRadius: 8,
                      background: "var(--accent-soft)", color: "var(--accent-hover)",
                      border: "1px solid var(--accent-border)", maxWidth: 260,
                    }}
                  >
                    <FileIcon className="size-3.5" style={{ flex: "none" }} />
                    <span style={{ overflow: "hidden", textOverflow: "ellipsis", whiteSpace: "nowrap" }}>
                      {p.replace(/^uploads\//, "")}
                    </span>
                    <XIcon
                      className="size-3.5"
                      style={{ cursor: "pointer", flex: "none" }}
                      onClick={() => removeAttachment(p)}
                    />
                  </span>
                ))}
              </div>
            )}
            <PromptInput onSubmit={onSubmit}>
              <PromptInputTextarea
                placeholder={
                  notReady
                    ? "Configure LLM in Settings first…"
                    : "Message SuperAI…  (drag files in, or 📎, then ask)"
                }
                disabled={chat.sending}
              />
              <PromptInputToolbar>
                <PromptInputTools>
                  <Button
                    type="button"
                    variant="ghost"
                    size="sm"
                    className="text-muted-foreground"
                    onClick={pickFiles}
                    disabled={chat.sending || importing}
                    title="Attach files (Word / Excel / PPT / PDF / image)"
                  >
                    <PaperclipIcon className="size-3.5" />
                    {importing ? "Importing…" : "Attach"}
                  </Button>
                  <Button
                    type="button"
                    variant="ghost"
                    size="sm"
                    className="text-muted-foreground"
                    onClick={() => chat.clear()}
                    disabled={chat.messages.length === 0 || chat.sending}
                    title="Clear conversation"
                  >
                    <Trash2Icon className="size-3.5" />
                    Clear
                  </Button>
                </PromptInputTools>
                <PromptInputSubmit
                  status={chat.sending ? "streaming" : "ready"}
                  disabled={chat.sending}
                />
              </PromptInputToolbar>
            </PromptInput>
          </div>
        </div>
        <TracePanel trace={chat.trace} />
      </div>
    </div>
  );
}
