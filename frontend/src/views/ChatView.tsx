import React, { useCallback, useEffect, useState } from "react";
import { useChat } from "../lib/useChat";
import { useAttachments } from "../lib/useAttachments";
import { AppStatus } from "../lib/types";
import { copyText } from "../lib/format";
import AttachmentChips from "../components/AttachmentChips";
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
import { CopyIcon, MessageSquareIcon, Trash2Icon, PaperclipIcon } from "lucide-react";
import { HistoryBar, HistoryList, useHistory } from "../components/HistoryBar";
import TracePanel from "../components/TracePanel";
import DeliverablesBar from "../components/DeliverablesBar";
import AgentProgress from "../components/AgentProgress";

export default function ChatView({ status }: { status: AppStatus | null }) {
  const chat = useChat();
  const attach = useAttachments();
  const notReady = status !== null && !status.ready;
  const messages = chat.messages;
  const history = useHistory();
  const [filesKey, setFilesKey] = useState(0);
  useEffect(() => {
    // A finished turn may have written files and definitely changed history.
    chat.onDone(() => {
      history.invalidate();
      setFilesKey((k) => k + 1);
    });
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  // Asking again while an answer is still streaming is allowed: each ask gets
  // its own bubble, so there is nothing to wait for.
  const onSubmit = (msg: { text: string }, e: React.FormEvent<HTMLFormElement>) => {
    const text = msg.text.trim();
    if (!text && attach.paths.length === 0) return;
    // Images go straight to the vision model as multimodal input; docs are read
    // via read_document from their workspace path referenced in the text.
    const { payload, images } = attach.build(text);
    attach.clear();
    chat.send(payload, images);
    e.currentTarget.reset();
  };

  return (
    <div className="view">
      <div className="chat-layout">
        <div className="chat-main" style={{ ["--wails-drop-target" as any]: "drop" }}>
          {/* Starting a new conversation mid-answer is safe: the running ask
              finishes into its own conversation's history. */}
          <HistoryBar history={history} onNew={chat.newSession} />

          {history.sessions ? (
            <HistoryList
              history={history}
              currentId={chat.sessionId}
              onPick={(id) => chat.loadSession(id).catch(() => {})}
            />
          ) : (
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
              <ConversationContent
                autoScrollKey={messages
                  .map((m) => `${m.content}#${m.progress?.length ?? 0}`)
                  .join("|")}
              >
                {messages.map((m) => (
                  <div key={m.id}>
                    <Message from={m.role}>
                      <MessageContent variant="flat">
                        {m.role === "assistant" ? (
                          <>
                            {m.progress && m.progress.length > 0 && (
                              <AgentProgress
                                steps={m.progress}
                                running={!!m.streaming}
                                startedAt={m.startedAt}
                                finishedAt={m.finishedAt}
                              />
                            )}
                            {m.content ? (
                              <Response>{m.content}</Response>
                            ) : m.streaming ? (
                              // The progress block carries its own spinner, so
                              // the caret is only needed before the first sign
                              // of life.
                              !m.progress?.length && <span className="msg-cursor" />
                            ) : (
                              !m.error && !m.progress?.length && "…"
                            )}
                            {m.error && <div className="msg-error">⚠ {m.error}</div>}
                          </>
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
          )}

          <DeliverablesBar sessionId={chat.sessionId} refreshKey={filesKey} />

          <div className="composer">
            <AttachmentChips paths={attach.paths} onRemove={attach.remove} />
            <PromptInput onSubmit={onSubmit}>
              <PromptInputTextarea
                placeholder={
                  notReady
                    ? "Configure LLM in Settings first…"
                    : "Message SuperAI…  (drag files in, or 📎, then ask)"
                }
              />
              <PromptInputToolbar>
                <PromptInputTools>
                  <Button
                    type="button"
                    variant="ghost"
                    size="sm"
                    className="text-muted-foreground"
                    onClick={attach.pick}
                    disabled={attach.importing}
                    title="Attach files (Word / Excel / PPT / PDF / image)"
                  >
                    <PaperclipIcon className="size-3.5" />
                    {attach.importing ? "Importing…" : "Attach"}
                  </Button>
                  <Button
                    type="button"
                    variant="ghost"
                    size="sm"
                    className="text-muted-foreground"
                    onClick={() => chat.clear()}
                    disabled={chat.messages.length === 0}
                    title="Clear conversation"
                  >
                    <Trash2Icon className="size-3.5" />
                    Clear
                  </Button>
                </PromptInputTools>
                {/* Stays enabled while streaming — the button is how a second
                    question gets asked. */}
                <PromptInputSubmit status="ready" />
              </PromptInputToolbar>
            </PromptInput>
          </div>
        </div>
        <TracePanel trace={chat.trace} asks={chat.asks} />
      </div>
    </div>
  );
}
