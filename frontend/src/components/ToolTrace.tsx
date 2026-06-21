import React from "react";
import { TraceItem, briefArgs } from "../lib/types";

interface Props {
  trace: TraceItem[];
  title?: string;
}

function StatusMark({ status }: { status: TraceItem["status"] }) {
  if (status === "ok") return <span className="tr-status tr-ok">✓</span>;
  if (status === "fail") return <span className="tr-status tr-fail">✗</span>;
  return <span className="tr-status" style={{ color: "var(--text-3)" }}>•••</span>;
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
          <div className="trace-empty">No tool activity yet.<br />Tools run during a task appear here live.</div>
        ) : (
          trace.map((t) => (
            <div key={t.id} className={`trace-row${t.inner ? " inner" : ""}`}>
              <span className="tr-arrow">{t.inner ? "↳" : "▶"}</span>
              <span className="tr-tool">{t.tool}</span>
              <span className="tr-args">{briefArgs(t.args)}</span>
              <StatusMark status={t.status} />
            </div>
          ))
        )}
      </div>
    </div>
  );
}
