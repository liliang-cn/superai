import React, { useEffect, useState } from "react";
import { PreviewPrompt } from "../../wailsjs/go/main/App";
import { backend } from "../../wailsjs/go/models";
import { ChevronDown, ChevronRight } from "lucide-react";

/**
 * What the model is about to be told, before it is told.
 *
 * A chat turn's first request is assembled from a dozen places — persona,
 * recalled memory, a plan a previous run left behind, extension-contributed
 * context, a skill reminder, the filtered history, and the tools that survived
 * the policies. This is that assembly, read-only: no Send from inside it, and
 * nothing here starts a run, because the whole point is to look before paying
 * for a turn.
 *
 * Sections collapse because the system prompt alone is thousands of tokens and
 * the question is usually about one part of it.
 */

function Section({
  title,
  meta,
  defaultOpen,
  children,
}: {
  title: string;
  meta?: React.ReactNode;
  defaultOpen?: boolean;
  children: React.ReactNode;
}) {
  const [open, setOpen] = useState(Boolean(defaultOpen));
  return (
    <div className="pp-section">
      <button className="pp-section-h" onClick={() => setOpen((o) => !o)}>
        {open ? <ChevronDown size={12} /> : <ChevronRight size={12} />}
        <span className="pp-section-t">{title}</span>
        {meta && <span className="pp-section-m">{meta}</span>}
      </button>
      {open && <div className="pp-section-b">{children}</div>}
    </div>
  );
}

/**
 * The preview, as a panel body.
 *
 * It used to be a modal over the whole window, which is the wrong shape for the
 * thing it shows: you look at the assembled turn *while* editing the message
 * that produced it, and a dialog covers the message. It lives in the rail
 * beside the tool trace now, and follows the draft as it is typed.
 */
export function PromptPreviewBody({
  sessionId,
  goal,
}: {
  sessionId: string;
  /** The draft as it stands. Debounced by the caller — assembling a turn is a
   *  round trip through the whole persona, memory and tool catalogue, and it is
   *  not worth doing per keystroke. */
  goal: string;
}) {
  const [p, setP] = useState<backend.PromptPreview | null>(null);
  const [busy, setBusy] = useState(true);
  const [err, setErr] = useState("");

  useEffect(() => {
    let live = true;
    setBusy(true);
    PreviewPrompt(sessionId, goal)
      .then((r) => {
        if (live) {
          setP(r);
          setErr("");
        }
      })
      .catch((e) => live && setErr(String(e)))
      .finally(() => live && setBusy(false));
    return () => {
      live = false;
    };
  }, [sessionId, goal]);

  // The first system message is the persona and its sections; any further
  // system messages were contributed on the way past — a hook, an extension,
  // a structured-output hint — and are worth telling apart from it.
  const systemMessages = (p?.messages ?? []).filter((m) => m.role === "system");
  const contributed = systemMessages.slice(1);
  const conversation = (p?.messages ?? []).filter((m) => m.role !== "system");
  const problem = err || p?.error || "";

  return (
    <>
      <div className="trace-head">
        <span>Prompt preview</span>
        <span style={{ color: "var(--text-3)", fontWeight: 400 }}>nothing is sent</span>
      </div>
      <div className="pp-body">
          {busy && <div className="loading-row"><span className="spinner" /> assembling the turn…</div>}

          {!busy && problem && <div className="pp-problem">⚠ {problem}</div>}

          {!busy && p && !problem && (
            <>
              <div className="pp-facts">
                <span><b>{p.estimatedTokens.toLocaleString()}</b> est. tokens</span>
                <span><b>{p.tools.length}</b> tools</span>
                <span><b>{p.messages.length}</b> messages</span>
                <span title={p.model}>{p.model || "default model"}</span>
              </div>
              <div className="pp-note">
                The estimate covers the messages only — this agent puts its whole tool catalogue in
                the schema, and those bytes are not counted here.
              </div>

              <Section title="System context" meta={`${p.systemPrompt.length} chars`} defaultOpen>
                <pre>{p.systemPrompt || "(empty)"}</pre>
              </Section>

              <Section
                title="Contributed sections"
                meta={contributed.length ? `${contributed.length}` : "none"}
              >
                {contributed.length === 0 ? (
                  <div className="pp-empty">
                    Nothing was appended by a hook or an extension for this turn.
                  </div>
                ) : (
                  contributed.map((m, i) => <pre key={i}>{m.content}</pre>)
                )}
              </Section>

              <Section title="Conversation" meta={`${conversation.length}`}>
                {conversation.length === 0 ? (
                  <div className="pp-empty">No history — this would be the first turn.</div>
                ) : (
                  conversation.map((m, i) => (
                    <div key={i} className="pp-msg">
                      <span className="pp-role">{m.role}</span>
                      <pre>{m.content || "(no text)"}</pre>
                    </div>
                  ))
                )}
              </Section>

              <Section title="Tools offered" meta={`${p.tools.length}`}>
                {p.tools.length === 0 ? (
                  <div className="pp-empty">
                    No tools. {p.forbidTools ? "This run's constraints forbid them." : ""}
                  </div>
                ) : (
                  <div className="pp-tools">
                    {p.tools.map((t) => (
                      <code key={t}>{t}</code>
                    ))}
                  </div>
                )}
              </Section>

              <Section
                title="Constraints"
                meta={p.constraintExtractionSkipped ? "not resolved" : p.constraintsDeclared ? "declared" : "none"}
                defaultOpen={p.constraintsDeclared || p.forbidTools}
              >
                {p.constraintExtractionSkipped ? (
                  <div className="pp-empty">
                    A real run resolves its constraints with one temperature-0 call to the model. A
                    preview does not call the model, so these are unknown until the turn is sent.
                  </div>
                ) : p.constraintsDeclared ? (
                  <div className="pp-msg">
                    {p.forbidTools && <pre>tools forbidden — the run would be offered none</pre>}
                    {p.deliverables.map((d, i) => (
                      <pre key={i}>must deliver — {d}</pre>
                    ))}
                  </div>
                ) : (
                  <div className="pp-empty">None declared for this turn.</div>
                )}
              </Section>
            </>
          )}
      </div>
    </>
  );
}
