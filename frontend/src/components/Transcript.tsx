import React, { useEffect, useRef } from "react";
import { ChatMessage } from "../lib/types";

function Bubble({ msg }: { msg: ChatMessage }) {
  const isUser = msg.role === "user";
  return (
    <div className={`msg ${msg.role}`}>
      <div className="msg-avatar">{isUser ? "U" : "AI"}</div>
      <div style={{ minWidth: 0 }}>
        <div className="msg-body">
          {msg.content || (msg.streaming ? "" : "…")}
          {msg.streaming && <span className="msg-cursor" />}
        </div>
        {msg.emotion && !msg.streaming && (
          <div className="emotion-chip">🎭 {msg.emotion}</div>
        )}
      </div>
    </div>
  );
}

interface Props {
  messages: ChatMessage[];
  className?: string;
  empty?: React.ReactNode;
}

export default function Transcript({ messages, className, empty }: Props) {
  const endRef = useRef<HTMLDivElement>(null);
  useEffect(() => {
    endRef.current?.scrollIntoView({ behavior: "smooth", block: "end" });
  }, [messages]);

  if (messages.length === 0 && empty) {
    return <div className={className}>{empty}</div>;
  }

  return (
    <div className={className}>
      {messages.map((m) => (
        <Bubble key={m.id} msg={m} />
      ))}
      <div ref={endRef} />
    </div>
  );
}
