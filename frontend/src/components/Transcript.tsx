import React, { useEffect, useRef, useState } from "react";
import ReactMarkdown from "react-markdown";
import remarkGfm from "remark-gfm";
import { ChatMessage } from "../lib/types";
import { copyText } from "../lib/format";
import { isExternallyOpenable, openExternal } from "../lib/openExternal";

function CodeBlock({ className, text }: { className?: string; text: string }) {
  const [copied, setCopied] = useState(false);
  const lang = (className || "").replace("language-", "");
  const copy = async () => {
    if (await copyText(text)) {
      setCopied(true);
      setTimeout(() => setCopied(false), 1200);
    }
  };
  return (
    <div className="codeblock">
      <div className="codeblock-bar">
        <span className="cb-lang">{lang || "text"}</span>
        <button className="cb-copy" onClick={copy}>{copied ? "✓ copied" : "copy"}</button>
      </div>
      <pre>
        <code className={className}>{text}</code>
      </pre>
    </div>
  );
}

const mdComponents = {
  code({ inline, className, children, ...props }: any) {
    if (inline) {
      return (
        <code className={className} {...props}>
          {children}
        </code>
      );
    }
    return <CodeBlock className={className} text={String(children).replace(/\n$/, "")} />;
  },
  // CodeBlock provides its own <pre>; unwrap react-markdown's outer <pre>.
  pre({ children }: any) {
    return <>{children}</>;
  },
  // A link the model wrote must leave the app rather than navigate it. Left as
  // a real anchor so it still looks like one and the URL shows on hover; the
  // click is what changes.
  a({ href, children, ...props }: any) {
    if (!isExternallyOpenable(href)) {
      return <span {...props}>{children}</span>;
    }
    return (
      <a
        href={href}
        title={href}
        onClick={(e) => {
          e.preventDefault();
          openExternal(href);
        }}
        {...props}
      >
        {children}
      </a>
    );
  },
};

function Bubble({ msg }: { msg: ChatMessage }) {
  const isUser = msg.role === "user";
  return (
    <div className={`msg ${msg.role}`}>
      <div className="msg-avatar">{isUser ? "U" : "AI"}</div>
      <div style={{ minWidth: 0 }}>
        <div className="msg-body">
          {isUser ? (
            <span style={{ whiteSpace: "pre-wrap" }}>{msg.content}</span>
          ) : msg.content ? (
            <div className="md">
              <ReactMarkdown remarkPlugins={[remarkGfm]} components={mdComponents}>
                {msg.content}
              </ReactMarkdown>
            </div>
          ) : msg.streaming ? (
            ""
          ) : (
            "…"
          )}
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
