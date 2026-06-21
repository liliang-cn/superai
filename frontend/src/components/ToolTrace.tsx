import React, { useState } from "react";
import { TraceItem, briefArgs } from "../lib/types";
import { copyText, highlightJson, prettyJson } from "../lib/format";

interface Props {
  trace: TraceItem[];
  title?: string;
}

function StatusMark({ status }: { status: TraceItem["status"] }) {
  if (status === "ok") return <span className="tr-status tr-ok">✓</span>;
  if (status === "fail") return <span className="tr-status tr-fail">✗</span>;
  return <span className="tr-status" style={{ color: "var(--text-3)" }}>•••</span>;
}

function Row({ t }: { t: TraceItem }) {
  const [open, setOpen] = useState(false);
  const [copied, setCopied] = useState(false);
  const payload = t.result !== undefined ? t.result : t.args;
  const hasDetail =
    payload !== undefined &&
    payload !== null &&
    (typeof payload !== "object" || Object.keys(payload).length > 0);

  const copy = async (e: React.MouseEvent) => {
    e.stopPropagation();
    if (await copyText(prettyJson(payload))) {
      setCopied(true);
      setTimeout(() => setCopied(false), 1200);
    }
  };

  return (
    <div className={`trace-row${t.inner ? " inner" : ""}${hasDetail ? " clickable" : ""}`}>
      <div className="tr-line" onClick={() => hasDetail && setOpen((o) => !o)}>
        {hasDetail ? <span className="tr-caret">{open ? "▾" : "▸"}</span> : <span className="tr-caret ph" />}
        <span className="tr-arrow">{t.inner ? "↳" : "▶"}</span>
        <span className="tr-tool">{t.tool}</span>
        <span className="tr-args">{briefArgs(t.args)}</span>
        <StatusMark status={t.status} />
      </div>
      {open && hasDetail && (
        <div className="tr-detail">
          <button className="tr-copy" onClick={copy}>{copied ? "copied" : "copy"}</button>
          <pre className="tr-json" dangerouslySetInnerHTML={{ __html: highlightJson(payload) }} />
        </div>
      )}
    </div>
  );
}

export default function ToolTrace({ trace, title = "Tool Trace" }: Props) {
  return (
    <div className="trace-panel">
      <div className="trace-head">
        <span>{title}</span>
        <span style={{ color: "var(--text-3)", fontWeight: 400 }}>{trace.length}</span>
      </div>
      <div className="trace-list">
        {trace.length === 0 ? (
          <div className="trace-empty">
            No tool activity yet.
            <br />
            Tools run during a task appear here live.
          </div>
        ) : (
          trace.map((t) => <Row key={t.id} t={t} />)
        )}
      </div>
    </div>
  );
}
