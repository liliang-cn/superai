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
import {
  CopyIcon,
  MessageSquareIcon,
  Trash2Icon,
  PaperclipIcon,
  SquareIcon,
} from "lucide-react";
import { HistoryBar, HistoryList, useHistory } from "../components/HistoryBar";
import TracePanel from "../components/TracePanel";
import DeliverablesBar from "../components/DeliverablesBar";
import ContextBlock from "../components/ContextBlock";
import AgentProgress from "../components/AgentProgress";

export default function ChatView({
  status,
  openSession,
  onSessionOpened,
}: {
  status: AppStatus | null;
  /** A conversation to restore on arrival — a scheduled run's, for instance. */
  openSession?: string;
  onSessionOpened?: () => void;
}) {
  const chat = useChat();
  const attach = useAttachments();
  const notReady = status !== null && !status.ready;
  const messages = chat.messages;
  const history = useHistory();
  const [filesKey, setFilesKey] = useState(0);
  // The composer is controlled so the send button can know whether there is
  // anything to send — which is what decides between "send" and "stop" below.
  const [draft, setDraft] = useState("");

  // Loading it here rather than in App keeps the transcript the only thing that
  // owns a session id. Picking a run's conversation is the same act as picking
  // one out of history.
  useEffect(() => {
    if (!openSession) return;
    history.close();
    chat.loadSession(openSession).catch(() => {});
    onSessionOpened?.();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [openSession]);

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
  const onSubmit = (msg: { text: string }) => {
    const text = msg.text.trim();
    if (!text && attach.paths.length === 0) return;
    // Images go straight to the vision model as multimodal input; docs are read
    // via read_document from their workspace path referenced in the text.
    const { payload, images } = attach.build(text);
    attach.clear();
    chat.send(payload, images);
    setDraft("");
  };

  return (
    <div className="view">
      <div className="chat-layout">
        <div
          className="chat-main"
          style={{ ["--wails-drop-target" as any]: "drop" }}
        >
          {/* Starting a new conversation mid-answer is safe: the running ask
              finishes into its own conversation's history. */}
          <HistoryBar
            history={history}
            onNew={chat.newSession}
            sessionId={chat.sessionId}
          />

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
                      ⚙️ Backend not ready — configure your LLM in Settings to
                      begin.
                    </div>
                  )}
                </ConversationEmptyState>
              ) : (
                <ConversationContent
                  autoScrollKey={messages
                    .map((m) => `${m.content}#${m.progress?.length ?? 0}`)
                    .join("|")}
                >
                  {messages.map((m) =>
                    m.kind === "context" ? (
                      <ContextBlock key={m.id} content={m.content} />
                    ) : (
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
                                  !m.progress?.length && (
                                    <span className="msg-cursor" />
                                  )
                                ) : (
                                  !m.error && !m.progress?.length && "…"
                                )}
                                {/* A stop is the user's own doing, so it is
                                    reported as an outcome and not as a fault —
                                    and whatever was already streamed stays. */}
                                {m.cancelled && (
                                  <div className="msg-stopped">
                                    ■ Stopped — you cancelled this answer.
                                  </div>
                                )}
                                {m.stopping && !m.cancelled && (
                                  <div className="msg-stopped">Stopping…</div>
                                )}
                                {m.error && !m.cancelled && (
                                  <div className="msg-error">⚠ {m.error}</div>
                                )}
                              </>
                            ) : (
                              <span style={{ whiteSpace: "pre-wrap" }}>
                                {m.content}
                              </span>
                            )}
                          </MessageContent>
                        </Message>
                        {/* One row under the reply: the copy action and the
                            mood chip are both about the message that just
                            finished, and stacking them made two half-empty
                            lines out of one. */}
                        {m.role === "assistant" &&
                          !m.streaming &&
                          (m.content || m.emotion) && (
                            <div className="msg-footer">
                              {m.content && (
                                <Actions>
                                  <Action
                                    label="Copy"
                                    tooltip="Copy message"
                                    onClick={() => copyText(m.content)}
                                  >
                                    <CopyIcon className="size-3.5" />
                                  </Action>
                                </Actions>
                              )}
                              {m.emotion && (
                                <div className="emotion-chip">🎭 {m.emotion}</div>
                              )}
                            </div>
                          )}
                      </div>
                    ),
                  )}
                </ConversationContent>
              )}
            </Conversation>
          )}

          <DeliverablesBar sessionId={chat.sessionId} refreshKey={filesKey} />

          <div className="composer">
            <AttachmentChips paths={attach.paths} onRemove={attach.remove} />
            <PromptInput onSubmit={onSubmit}>
              <PromptInputTextarea
                value={draft}
                onChange={(e) => setDraft(e.target.value)}
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
                {/* While an answer is streaming and nothing has been typed, the
                    send button is a stop button — the same place ChatGPT and
                    Claude put it. Type anything and it turns back into send, so
                    asking a second question mid-answer (which this transcript
                    supports) is never taken away; ⏎ sends either way. */}
                {chat.sending && draft.trim() === "" ? (
                  <Button
                    type="button"
                    size="icon"
                    variant="secondary"
                    className="rounded-lg"
                    title="Stop generating"
                    aria-label="Stop generating"
                    onClick={() => chat.cancel()}
                  >
                    <SquareIcon className="size-3.5 fill-current" />
                  </Button>
                ) : (
                  <PromptInputSubmit status="ready" />
                )}
              </PromptInputToolbar>
            </PromptInput>
          </div>
        </div>
        <TracePanel trace={chat.trace} asks={chat.asks} />
      </div>
    </div>
  );
}
