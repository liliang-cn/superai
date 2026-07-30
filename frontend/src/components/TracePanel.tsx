import React, { useEffect, useMemo, useState } from "react";
import { PanelRightCloseIcon, PanelRightOpenIcon } from "lucide-react";
import { AskSummary, TraceItem } from "../lib/types";
import { CodeBlock } from "@/components/ai-elements/code-block";
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

/** The argument names a tool uses for source code the model wrote itself. */
const CODE_ARGS = ["code", "script"];

function codeLanguage(tool: string): string {
  const t = tool.toLowerCase();
  if (t.includes("javascript") || t.includes("_js")) return "javascript";
  if (t.includes("python") || t.includes("_py")) return "python";
  if (t.includes("shell") || t.includes("bash")) return "bash";
  return "text";
}

/**
 * Split a program the model wrote out of the rest of the arguments. Code is the
 * most interesting thing on an execute_javascript call and the least readable
 * as a JSON string, where it arrives as one line full of \n escapes.
 */
function splitCode(args: Record<string, any> | undefined): {
  code: string;
  rest: Record<string, any>;
} {
  const rest: Record<string, any> = {};
  let code = "";
  for (const [k, v] of Object.entries(args || {})) {
    if (!code && CODE_ARGS.includes(k) && typeof v === "string" && v.trim()) code = v;
    else rest[k] = v;
  }
  return { code, rest };
}

export function TraceTool({ t }: { t: TraceItem }) {
  const state = traceState(t.status);
  const r = t.result;
  const { code, rest } = splitCode(t.args);
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
        {code && (
          <div className="space-y-1 p-3">
            <h4 className="text-xs font-medium uppercase tracking-wide text-muted-foreground">
              Code
            </h4>
            <CodeBlock code={code} language={codeLanguage(t.tool)} className="my-0" />
          </div>
        )}
        {Object.keys(rest).length > 0 && (
          <ToolInput className={code ? "border-t border-border" : undefined} input={rest} />
        )}
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

const OPEN_KEY = "superai-trace-open";

interface TraceGroup {
  askId: string;
  ask?: AskSummary;
  items: TraceItem[];
}

/** Tool activity in arrival order, split by the ask that caused it. */
function groupByAsk(trace: TraceItem[], asks: AskSummary[]): TraceGroup[] {
  const groups: TraceGroup[] = [];
  const byAsk = new Map<string, TraceGroup>();
  for (const t of trace) {
    let g = byAsk.get(t.askId);
    if (!g) {
      g = { askId: t.askId, ask: asks.find((a) => a.id === t.askId), items: [] };
      byAsk.set(t.askId, g);
      groups.push(g);
    }
    g.items.push(t);
  }
  return groups;
}

export default function TracePanel({
  trace,
  asks = [],
  title = "Tool Trace",
}: {
  trace: TraceItem[];
  /** The asks the trace belongs to, used to label overlapping questions. */
  asks?: AskSummary[];
  title?: string;
}) {
  const [open, setOpen] = useState(() => localStorage.getItem(OPEN_KEY) !== "0");
  const groups = useMemo(() => groupByAsk(trace, asks), [trace, asks]);

  useEffect(() => {
    localStorage.setItem(OPEN_KEY, open ? "1" : "0");
  }, [open]);

  // Collapsed: a rail wide enough for the toggle, keeping the tool count
  // visible so activity while it is hidden is not silent.
  if (!open) {
    return (
      <div className="trace-panel collapsed">
        <button
          type="button"
          className="panel-toggle"
          onClick={() => setOpen(true)}
          title={`Show ${title.toLowerCase()}`}
          aria-label={`Show ${title.toLowerCase()}`}
        >
          <PanelRightOpenIcon className="size-4" />
          {trace.length > 0 && <span className="panel-badge">{trace.length}</span>}
        </button>
      </div>
    );
  }

  return (
    <div className="trace-panel">
      <div className="trace-head">
        <span>{title}</span>
        <span style={{ display: "flex", alignItems: "center", gap: 8 }}>
          <span style={{ color: "var(--text-3)", fontWeight: 400 }}>{trace.length}</span>
          <button
            type="button"
            className="panel-toggle inline"
            onClick={() => setOpen(false)}
            title={`Hide ${title.toLowerCase()}`}
            aria-label={`Hide ${title.toLowerCase()}`}
          >
            <PanelRightCloseIcon className="size-4" />
          </button>
        </span>
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
        ) : groups.length === 1 ? (
          // One question at a time is the normal case, and it reads better
          // without a header naming the only thing on screen.
          groups[0].items.map((t) => <TraceTool key={t.id} t={t} />)
        ) : (
          groups.map((g) => (
            <div className="trace-group" key={g.askId}>
              <div className="trace-group-head">
                <span className={`trace-dot ${g.ask?.status || "done"}`} />
                <span className="trace-group-label">
                  {g.ask?.prompt || "Earlier ask"}
                </span>
              </div>
              {g.items.map((t) => (
                <TraceTool key={t.id} t={t} />
              ))}
            </div>
          ))
        )}
      </div>
    </div>
  );
}
