import { ChatHistory, DeleteChatSession, SendChat } from "../../wailsjs/go/app/App";

/** What the hive made for a tile someone described in words. */
export interface GenSpec {
  prompt: string;
  title?: string;
  kind?: "number" | "bars" | "list";
  value?: string;
  caption?: string;
  items?: { label: string; value?: number; text?: string }[];
  updated?: string;
  error?: string;
}

const KEY = "superai-canvas-generated";

export function loadSpecs(): Record<string, GenSpec> {
  try {
    return JSON.parse(localStorage.getItem(KEY) || "{}");
  } catch {
    return {};
  }
}

export function saveSpecs(s: Record<string, GenSpec>) {
  try {
    localStorage.setItem(KEY, JSON.stringify(s));
  } catch {
    /* private window: the tiles still work for this visit */
  }
}

function parse(s: string): Omit<GenSpec, "prompt"> | null {
  const a = s.indexOf("{"), b = s.lastIndexOf("}");
  if (a < 0 || b <= a) return null;
  try {
    const o = JSON.parse(s.slice(a, b + 1));
    return { title: o.title ?? "", kind: o.kind ?? "list", value: o.value != null ? String(o.value) : undefined, caption: o.caption, items: o.items };
  } catch {
    return null;
  }
}

/**
 * One refresh of a described tile: a short order to the queen in a
 * conversation of its own, deleted once the answer is read so tiles do not
 * crowd the conversation list.
 *
 * The answer is asked for as a line saying TILE and then the JSON: agent-go
 * drops a final reply that is nothing but JSON as "not an answer", and the
 * conversation then ends with no reply at all.
 */
export async function makeTile(prompt: string, context: string): Promise<Omit<GenSpec, "prompt">> {
  const session = `tile-${Date.now().toString(36)}-${Math.random().toString(36).slice(2, 10)}`;
  const ask = `You are filling one small tile on the user's screen. They asked for: «${prompt}».
Here is what the hive looks like right now; answer from it whenever it is enough, and use tools only for what it does not cover, no more than three calls:
${context}
Answer with the word TILE on a line of its own, then the JSON on the next line, and nothing else, no code fences:
{"title": "<4 words at most>", "kind": "number" | "bars" | "list", "value": "<the number, for kind number>", "caption": "<one short line>", "items": [{"label": "<short>", "value": <number, for bars>, "text": "<short, for list>"}]}
Use number for a single figure, bars for up to 6 comparable figures, list for up to 5 short facts. Answer in the language they asked in.`;
  try {
    await SendChat(session, ask, []);
    for (let i = 0; i < 80; i++) {
      await new Promise((r) => setTimeout(r, 3000));
      const turns = ((await ChatHistory(session)) ?? []) as any[];
      const last = [...turns].reverse().find((t) => t.role === "assistant");
      const made = last ? parse(String(last.content ?? "")) : null;
      if (made) return { ...made, updated: new Date().toISOString() };
    }
    return { error: "The hive did not answer. Try again." };
  } catch (e: any) {
    return { error: String(e?.message || e) };
  } finally {
    DeleteChatSession(session).catch(() => {});
  }
}
