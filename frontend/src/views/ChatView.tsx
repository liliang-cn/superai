import React from "react";
import { useChat } from "../lib/useChat";
import { AppStatus } from "../lib/types";
import { copyText } from "../lib/format";
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
import { CopyIcon, MessageSquareIcon, Trash2Icon } from "lucide-react";
import ToolTrace from "../components/ToolTrace";

export default function ChatView({ status }: { status: AppStatus | null }) {
  const chat = useChat();
  const notReady = status !== null && !status.ready;

  const onSubmit = (msg: { text: string }, e: React.FormEvent<HTMLFormElement>) => {
    const text = msg.text.trim();
    if (!text || chat.sending) return;
    chat.send(text);
    e.currentTarget.reset();
  };

  return (
    <div className="view">
      <div className="chat-layout">
        <div className="chat-main">
          <Conversation className="transcript">
            {chat.messages.length === 0 ? (
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
            <PromptInput onSubmit={onSubmit}>
              <PromptInputTextarea
                placeholder={
                  notReady
                    ? "Configure LLM in Settings first…"
                    : "Message SuperAI…  (Enter to send, Shift+Enter for newline)"
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
        <ToolTrace trace={chat.trace} />
      </div>
    </div>
  );
}
