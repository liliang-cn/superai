/** The trace id in a flow event's traceparent ("00-<trace>-<span>-01"). */
export function traceIdOf(traceparent?: string): string {
  const id = (traceparent ?? "").split("-")[1] ?? "";
  return /^[0-9a-f]{32}$/.test(id) ? id : "";
}
