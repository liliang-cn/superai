import { ClipboardSetText } from "../../wailsjs/runtime";

// copyText copies via the Wails clipboard runtime, falling back to the Web API.
export async function copyText(s: string): Promise<boolean> {
  try {
    if (await ClipboardSetText(s)) return true;
  } catch {
    /* fall through */
  }
  try {
    await navigator.clipboard.writeText(s);
    return true;
  } catch {
    return false;
  }
}

// parseTime accepts both shapes the backend sends a moment in: RFC3339 from a
// marshalled time.Time, and "2006-01-02 15:04:05" from the scheduled-run event.
// A space between date and time is not a format engines must parse, so it is
// normalised away first. A time.Time that was never set marshals as year 1,
// which is not a moment worth showing anywhere, so it reads as absent too.
export function parseTime(value: any): Date | null {
  const raw = typeof value === "string" ? value.trim() : "";
  if (raw === "") return null;
  for (const candidate of [raw, raw.replace(" ", "T")]) {
    const d = new Date(candidate);
    if (!Number.isNaN(d.getTime()) && d.getFullYear() > 1970) return d;
  }
  return null;
}

// formatStamp renders a moment in the platform's locale, to the minute.
export function formatStamp(d: Date): string {
  return d.toLocaleString(undefined, {
    weekday: "short",
    month: "short",
    day: "numeric",
    hour: "2-digit",
    minute: "2-digit",
  });
}

// fromNow is the other half of a timestamp: the absolute time answers "when",
// this answers "is that what I meant".
export function fromNow(d: Date, now: number = Date.now()): string {
  const delta = d.getTime() - now;
  const mins = Math.round(Math.abs(delta) / 60000);
  let text: string;
  if (mins < 1) text = "less than a minute";
  else if (mins < 60) text = `${mins} min`;
  else if (mins < 60 * 24) {
    const h = Math.round(mins / 60);
    text = `${h} hour${h === 1 ? "" : "s"}`;
  } else {
    const days = Math.round(mins / (60 * 24));
    text = `${days} day${days === 1 ? "" : "s"}`;
  }
  return delta < 0 ? `${text} ago` : `in ${text}`;
}

// formatDuration renders a wall clock reading for a finished run.
export function formatDuration(ms: number): string {
  if (!Number.isFinite(ms) || ms < 0) return "—";
  if (ms < 1000) return `${Math.round(ms)}ms`;
  const s = ms / 1000;
  if (s < 60) return `${s.toFixed(s < 10 ? 1 : 0)}s`;
  const m = Math.floor(s / 60);
  return `${m}m ${Math.round(s - m * 60)}s`;
}

// firstLine collapses a multi-line string to a single labelled line.
export function firstLine(s: string, max = 70): string {
  const line = (s || "").trim().split("\n")[0].trim();
  return line.length > max ? `${line.slice(0, max - 1)}…` : line;
}

function esc(s: string): string {
  return s.replace(/&/g, "&amp;").replace(/</g, "&lt;").replace(/>/g, "&gt;");
}

// prettyJson stringifies a value (objects pretty-printed; scalars as-is).
export function prettyJson(value: any): string {
  if (typeof value === "string") return value;
  try {
    return JSON.stringify(value, null, 2);
  } catch {
    return String(value);
  }
}

// highlightJson returns an HTML string with classed spans for keys/strings/
// numbers/booleans/null — the classic regex JSON highlighter (input escaped).
export function highlightJson(value: any): string {
  const json = esc(prettyJson(value));
  return json.replace(
    /("(\\u[a-zA-Z0-9]{4}|\\[^u]|[^\\"])*"(\s*:)?|\b(true|false|null)\b|-?\d+(?:\.\d+)?(?:[eE][+-]?\d+)?)/g,
    (m) => {
      let cls = "j-num";
      if (/^"/.test(m)) cls = /:$/.test(m) ? "j-key" : "j-str";
      else if (m === "true" || m === "false") cls = "j-bool";
      else if (m === "null") cls = "j-null";
      return `<span class="${cls}">${m}</span>`;
    },
  );
}

/**
 * What a streaming answer should show, given everything received so far.
 *
 * The model's stream carries its programmatic tool calls inline, as
 * `<code>…</code>` blocks — that is the protocol, not prose. Appending each
 * delta verbatim put those scripts in the answer bubble, so a turn that used
 * tools read as `const res = callTool('bash', …)` until it finished and the
 * final answer replaced it. The Tool Trace panel already shows every script
 * with its result, which is where a script belongs.
 *
 * Takes the whole accumulated text rather than one delta because a block is
 * split across deltas at arbitrary points: an opening tag can arrive in one
 * chunk and its closing tag several later. Until the closing tag lands, the
 * script is suppressed rather than shown half-written.
 *
 * Markdown fences are left alone — ```js is how an answer legitimately shows
 * code, and only the <code> tags mean "run this".
 */
export function visibleAnswer(raw: string): string {
  let out = raw.replace(/<code>[\s\S]*?<\/code>/g, "\n");
  const open = out.lastIndexOf("<code>");
  if (open >= 0) out = out.slice(0, open);
  // The trailing "情绪: 中性" tag is protocol too — the backend splits it off
  // the completed answer and routes it to the avatar. A streamed bubble never
  // saw that split, so the tag used to flash at the end of every answer; on a
  // stopped one it stayed there for good, because there is no final text to
  // replace it with. Only a short last line counts, so an answer that discusses
  // 情绪 in prose is left alone.
  out = out.replace(/\n?[ \t]*情绪[:：][^\n]{0,20}[ \t]*$/, "");
  return out.trim();
}
