export type ViewKey =
  | "chat"
  | "settings"
  | "avatar"
  | "memory"
  | "skills"
  | "mcp"
  | "data"
  | "life";

export interface ChatEvent {
  type: string;
  content: string;
  tool: string;
  args: Record<string, any>;
  result: any;
  debugType: string;
}

export interface ChatDone {
  final: string;
  emotion: string;
}

export interface ChatError {
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

export interface ChatMessage {
  id: string;
  role: "user" | "assistant";
  content: string;
  emotion?: string;
  streaming?: boolean;
}

export interface TraceItem {
  id: string;
  tool: string;
  args: Record<string, any>;
  inner: boolean;
  status: "running" | "ok" | "fail";
  result?: any;
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
