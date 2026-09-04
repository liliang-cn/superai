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
  RefreshCwIcon,
  MessageSquareIcon,
  Trash2Icon,
  PaperclipIcon,
  SquareIcon,
  LayoutDashboardIcon,
} from "lucide-react";
import { HistoryBar, HistoryList, useHistory } from "../components/HistoryBar";
import SidePanel from "../components/SidePanel";
import DeliverablesBar from "../components/DeliverablesBar";
import ContextBlock from "../components/ContextBlock";
import NameDashboardModal from "../components/NameDashboardModal";
import { dashboards, hasRenderableBlock, suggestName } from "../lib/dashboards";
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
  // The message index whose save just landed, for a moment of green.
  const [saved, setSaved] = useState(-1);
  // The reply waiting to be named. Null when the dialog is closed.
  const [naming, setNaming] = useState<{ index: number; content: string; prompt: string } | null>(null);
  const [saveError, setSaveError] = useState("");

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
  /**
   * Ask again for the answer at `index`, by re-sending the question that
   * produced it.
   *
   * The previous answer stays. Replacing it in place would be the tidier
   * screen and the worse record: a model asked twice gives two different
   * answers, and which one a person acted on is not recoverable from a
   * transcript that kept only the last.
   */
  /** The ask a reply answered: the nearest user message above it. */
  const askBehind = useCallback(
    (index: number): string => {
      for (let i = index - 1; i >= 0; i--) {
        const prev = messages[i];
        if (prev.role === "user" && prev.kind !== "context" && prev.content) {
          return prev.content;
        }
      }
      return "";
    },
    [messages],
  );

  const regenerate = (index: number) => {
    const prompt = askBehind(index);
    if (prompt) chat.send(prompt);
  };

  /**
   * Keep a reply, and the question behind it.
   *
   * The question is what makes a saved dashboard more than a screenshot: it is
   * what Refresh re-asks. So it is saved even though nothing here shows it —
   * and a reply with no ask above it (a scheduled run opened from Records, say)
   * still saves, it just cannot refresh itself, which the panel says.
   */
  const saveDashboard = (index: number, content: string) =>
    setNaming({ index, content, prompt: askBehind(index) });

  const commitDashboard = async (name: string) => {
    if (!naming) return;
    const { index, content, prompt } = naming;
    setNaming(null);
    try {
      await dashboards.save(name, content, prompt);
      setSaved(index);
      setTimeout(() => setSaved(-1), 1600);
    } catch (e: any) {
      setSaveError(String(e?.message || e));
      setTimeout(() => setSaveError(""), 4000);
    }
  };

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
                  {messages.map((m, mi) =>
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
                                  <Action
                                    label="Regenerate"
                                    tooltip="Ask again"
                                    disabled={chat.sending}
                                    onClick={() => regenerate(mi)}
                                  >
                                    <RefreshCwIcon className="size-3.5" />
                                  </Action>
                                  {/* Only on a reply that actually drew
                                      something. Prose is worth copying, not
                                      pinning to a wall. */}
                                  {hasRenderableBlock(m.content) && (
                                    <Action
                                      label="Save as dashboard"
                                      tooltip={
                                        saved === mi
                                          ? "Saved"
                                          : "Save as dashboard"
                                      }
                                      onClick={() => saveDashboard(mi, m.content)}
                                    >
                                      <LayoutDashboardIcon
                                        className="size-3.5"
                                        style={
                                          saved === mi
                                            ? { color: "var(--green)" }
                                            : undefined
                                        }
                                      />
                                    </Action>
                                  )}
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

          <div data-pet-spot="files" data-pet-label="the row of files this conversation produced">
            <DeliverablesBar sessionId={chat.sessionId} refreshKey={filesKey} />
          </div>

          <div className="composer" data-pet-spot="composer" data-pet-label="the box you type a message into">
            <AttachmentChips paths={attach.paths} onRemove={attach.remove} />
            <PromptInput onSubmit={onSubmit}>
              <PromptInputTextarea
                value={draft}
                onChange={(e) => setDraft(e.target.value)}
                onPaste={attach.paste}
                placeholder={
                  notReady
                    ? "Configure LLM in Settings first…"
                    : "Message SuperAI…  (drag files in, paste a screenshot, or 📎)"
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
                    data-pet-spot="attach"
                    data-pet-label="the paperclip that attaches a file"
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
                  <PromptInputSubmit status="ready" data-pet-spot="send" data-pet-label="the send button" />
                )}
              </PromptInputToolbar>
            </PromptInput>
          </div>
        </div>
        <SidePanel
          trace={chat.trace}
          asks={chat.asks}
          sessionId={chat.sessionId}
          draft={draft}
        />
      </div>
      {naming && (
        <NameDashboardModal
          suggested={suggestName(naming.prompt)}
          prompt={naming.prompt}
          onCancel={() => setNaming(null)}
          onSave={commitDashboard}
        />
      )}
      {saveError && <div className="nd-error">⚠ {saveError}</div>}
    </div>
  );
}
