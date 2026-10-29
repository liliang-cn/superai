/** The trace id in a flow event's traceparent ("00-<trace>-<span>-01"). */
export function traceIdOf(traceparent?: string): string {
  const id = (traceparent ?? "").split("-")[1] ?? "";
  return /^[0-9a-f]{32}$/.test(id) ? id : "";
}

/** The command a shell-like tool call runs, as one short line ("" for other tools). */
export function commandLine(args: unknown, max = 60): string {
  const c = (args as { command?: unknown } | null | undefined)?.command;
  if (typeof c !== "string") return "";
  const s = c.replace(/\s+/g, " ").trim();
  return s.length > max ? s.slice(0, max - 1) + "…" : s;
}

/** What a running call is called on screen: "bash · openclaw doctor --fix". */
export function stepLabel(tool: string, args?: unknown): string {
  const c = commandLine(args);
  return c ? `${tool} · ${c}` : tool;
}
