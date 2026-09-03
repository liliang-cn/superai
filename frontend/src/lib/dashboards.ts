import { Dashboards, DeleteDashboard, RefreshDashboard, RenameDashboard, SaveDashboard, SetDashboardCron } from "../../wailsjs/go/main/App";
import { backend } from "../../wailsjs/go/models";

/**
 * Saved dashboards, from the frontend's side.
 *
 * The store and the refreshing live in Go (backend/dashboards.go,
 * app_dashboards.go). This is the thin layer above them: which replies are
 * worth offering to save, and unwrapping the {ok, data} envelope the bound
 * methods answer with.
 */

export type Dashboard = backend.Dashboard;

/**
 * The fenced blocks the renderer actually draws — the same six the generation
 * rules teach the model, and the reason those rules and this list are both
 * built from the plugin set rather than typed out twice would be a good next
 * step; for now they are checked against `uiRules()` output by hand.
 *
 * A reply with none of these is prose. Prose is worth copying, not pinning to
 * a wall, so the save action does not appear on it.
 */
const RENDERABLE_BLOCKS = new Set(["bigscreen", "ui", "chart", "list", "mermaid", "sources"]);

/** A fenced block's opening line: ```name, at the start of a line. */
const FENCE = /^[ \t]*```([A-Za-z][\w-]*)/gm;

/** hasRenderableBlock reports whether a reply contains anything worth pinning. */
export function hasRenderableBlock(text: string): boolean {
  if (!text || !text.includes("```")) return false;
  FENCE.lastIndex = 0;
  for (let m = FENCE.exec(text); m; m = FENCE.exec(text)) {
    if (RENDERABLE_BLOCKS.has(m[1].toLowerCase())) return true;
  }
  return false;
}

/**
 * A first name for a dashboard, taken from the question that produced it.
 *
 * The question is what the person actually typed, so it reads like something
 * they would have called it. Trimmed to a label rather than a sentence; they
 * can rename it, and an editable default beats an empty box.
 */
export function suggestName(prompt: string): string {
  const line = (prompt || "").trim().split("\n")[0].trim();
  if (!line) return "Dashboard";
  // A question is usually an instruction with the subject buried in it —
  // "用一个 bigscreen 块做一个今日日程看板，标题「今日日程」。先查我今天的日程和提醒".
  // Taking its first forty characters put the instruction in the name box.
  // A quoted title is the model being told what to call it, so it wins; a
  // clause boundary is the next best guess at where the subject ends.
  const quoted = line.match(/[「“"']([^」”"']{1,24})[」”"']/);
  if (quoted) return quoted[1];
  const clause = line.split(/[，,。.；;：:!?！？\n]/).map((c) => c.trim()).filter(Boolean);
  const pick = clause.find((c) => c.length <= 24) ?? clause[0] ?? line;
  return pick.length > 24 ? pick.slice(0, 24).trimEnd() + "…" : pick;
}

/** The App methods answer {ok, data}; a rejection has to become a throw or a
 *  failed call reports success. */
function unwrap<T>(res: Record<string, any>): T {
  if (!res?.ok) throw new Error(String(res?.error ?? "the call failed"));
  return res.data as T;
}

export const dashboards = {
  list: (): Promise<Dashboard[]> => Dashboards(),
  save: async (name: string, source: string, prompt: string): Promise<Dashboard> =>
    unwrap<Dashboard>(await SaveDashboard(name, source, prompt)),
  rename: async (id: string, name: string): Promise<void> => {
    unwrap(await RenameDashboard(id, name));
  },
  remove: async (id: string): Promise<void> => {
    unwrap(await DeleteDashboard(id));
  },
  refresh: async (id: string): Promise<void> => {
    unwrap(await RefreshDashboard(id));
  },
  setCron: async (id: string, cron: string): Promise<void> => {
    unwrap(await SetDashboardCron(id, cron));
  },
};

/**
 * How old the contents are, in words.
 *
 * Always shown, never hidden when it is recent: the whole point of storing a
 * refreshed-at is that a wall of prices carries no clue in itself about which
 * day it is a picture of.
 */
export function ageLabel(iso: string): string {
  const then = new Date(iso).getTime();
  if (!then || Number.isNaN(then)) return "unknown age";
  const mins = Math.max(0, Math.round((Date.now() - then) / 60000));
  if (mins < 1) return "just now";
  if (mins < 60) return `${mins} min ago`;
  const hours = Math.round(mins / 60);
  if (hours < 24) return `${hours} h ago`;
  const days = Math.round(hours / 24);
  return `${days} d ago`;
}
