export type ViewKey =
  | "chat"
  | "settings"
  | "avatar"
  | "memory"
  | "skills"
  | "mcp"
  | "data"
  | "life";

/**
 * Every streaming payload is tagged with the id `SendChat` returned for that
 * ask. One conversation can have several asks in flight, and the tag is the
 * only thing that says which bubble an event belongs to.
 */
export interface ChatEvent {
  requestId: string;
  type: string;
  content: string;
  tool: string;
  args: Record<string, any>;
  result: any;
  debugType: string;
}

export interface ChatDone {
  requestId: string;
  final: string;
  emotion: string;
}

export interface ChatError {
  requestId: string;
  error: string;
}

export interface AppStatus {
  ready: boolean;
  error: string;
  skills: string[];
  memoryMode: string;
  browser: boolean;
  avatarPort: number;
}

/**
 * One line of "what the agent is doing" — the thinking and state_update events
 * the model emits between tokens. Without these the UI is silent for the whole
 * time a generated-code tool is being written and run.
 */
export interface ProgressStep {
  id: string;
  kind: "thinking" | "state" | "tool";
  text: string;
  /** Set for `tool` steps so the name can be rendered as code. */
  tool?: string;
}

export interface ChatMessage {
  id: string;
  role: "user" | "assistant";
  content: string;
  emotion?: string;
  streaming?: boolean;
  /** Progress lines captured while this ask was running. */
  progress?: ProgressStep[];
  /** This ask failed. Kept per message so one bad ask cannot blank a sibling. */
  error?: string;
  /**
   * Wall clock of the ask, used for the "worked for 21s" summary. Only bubbles
   * this client streamed have them, which also distinguishes a live ask from a
   * message restored out of history.
   */
  startedAt?: number;
  finishedAt?: number;
}

export interface TraceItem {
  id: string;
  /** The assistant message (i.e. the ask) whose turn ran this tool. */
  askId: string;
  tool: string;
  args: Record<string, any>;
  inner: boolean;
  status: "running" | "ok" | "fail";
  result?: any;
}

/** An ask, as far as anything outside the transcript needs to know about it. */
export interface AskSummary {
  id: string;
  /** The question that started it, for labelling its tool activity. */
  prompt: string;
  status: "streaming" | "done" | "error";
}

export function normalizeStatus(raw: Record<string, any> | null): AppStatus {
  return {
    ready: Boolean(raw?.ready),
    error: String(raw?.error ?? ""),
    skills: Array.isArray(raw?.skills) ? raw!.skills : [],
    memoryMode: String(raw?.memoryMode ?? "—"),
    browser: Boolean(raw?.browser),
    avatarPort: Number(raw?.avatarPort ?? 0),
  };
}

export function briefArgs(args: Record<string, any> | undefined | null): string {
  if (!args || typeof args !== "object") return "";
  const parts: string[] = [];
  for (const [k, v] of Object.entries(args)) {
    let val: string;
    if (v == null) val = "";
    else if (typeof v === "string") val = v;
    else if (typeof v === "object") val = JSON.stringify(v);
    else val = String(v);
    if (val.length > 48) val = val.slice(0, 45) + "…";
    parts.push(`${k}=${val}`);
    if (parts.join(" ").length > 70) break;
  }
  return parts.join(" ");
}
