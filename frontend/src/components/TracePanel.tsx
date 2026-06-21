import React from "react";
import { TraceItem } from "../lib/types";
import {
  Tool,
  ToolHeader,
  ToolContent,
  ToolInput,
  ToolOutput,
  ToolState,
} from "@/components/ai-elements/tool";

function traceState(status: TraceItem["status"]): ToolState {
  if (status === "ok") return "output-available";
  if (status === "fail") return "output-error";
  return "input-available";
}

export function TraceTool({ t }: { t: TraceItem }) {
  const state = traceState(t.status);
  const r = t.result;
  const errorText =
    state === "output-error"
      ? typeof r === "string"
        ? r
        : JSON.stringify(r?.error ?? r, null, 2)
      : undefined;
  return (
    <Tool className={t.inner ? "ml-3 border-dashed" : ""}>
      <ToolHeader type={(t.inner ? "↳ " : "") + t.tool} state={state} />
      <ToolContent>
        {t.args && Object.keys(t.args).length > 0 && <ToolInput input={t.args} />}
        {state !== "input-available" && (
          <ToolOutput
            errorText={errorText}
            output={
              errorText ? undefined : (
                <pre>
                  <code>
                    {typeof r === "string" ? r : JSON.stringify(r, null, 2)}
                  </code>
                </pre>
              )
            }
          />
        )}
      </ToolContent>
    </Tool>
  );
}

export default function TracePanel({
  trace,
  title = "Tool Trace",
}: {
  trace: TraceItem[];
  title?: string;
}) {
  return (
    <div className="trace-panel">
      <div className="trace-head">
        <span>{title}</span>
        <span style={{ color: "var(--text-3)", fontWeight: 400 }}>{trace.length}</span>
      </div>
      <div
        className="trace-list"
        style={{ display: "flex", flexDirection: "column", gap: 8, padding: 8 }}
      >
        {trace.length === 0 ? (
          <div className="trace-empty">
            No tool activity yet.
            <br />
            Tools run during a task appear here live.
          </div>
        ) : (
          trace.map((t) => <TraceTool key={t.id} t={t} />)
        )}
      </div>
    </div>
  );
}
