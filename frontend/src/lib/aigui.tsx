import { useEffect, useState } from "react";
import { createRoot } from "react-dom/client";
import {
  ActionRegistry,
  ActionRuntime,
  CardRegistry,
  buildSystemPrompt,
  type NodeRenderer,
} from "@ai-gui/core";
import { primitives } from "@ai-gui/plugin-primitives";
import { katex } from "@ai-gui/plugin-katex";
import { mermaid } from "@ai-gui/plugin-mermaid";
import { chart } from "@ai-gui/plugin-chart";
import { citation } from "@ai-gui/plugin-citation";
import { bigscreen } from "@ai-gui/plugin-bigscreen";
import { ui } from "@ai-gui/plugin-ui";
import { CodeBlock } from "@/components/ai-elements/code-block";
import { AddRecord, AddSchedule } from "../../wailsjs/go/main/App";

/**
 * The language SuperAI speaks. The persona answers in Chinese, so the block
 * rules handed to the model and the labels plugins draw follow it.
 */
export const APP_LOCALE = "zh-CN";

/**
 * The card registry the assistant may render into. Cards are app-defined: the
 * model only fills data that matches a type registered here, so an empty
 * registry means "markdown and the plugin blocks below, nothing bespoke".
 * Register app cards here as they are built.
 */
export const registry = new CardRegistry();

/**
 * What a `ui` block is allowed to invoke.
 *
 * The plugin refuses any button or form whose action is not registered here,
 * and the rules handed to the model list these names, so the set below is the
 * whole vocabulary — an unregistered action does not fail on its own, it
 * invalidates the entire block it appears in.
 *
 * Each one is added by hand and wired to the App method that performs it. There
 * is deliberately no passthrough that would let the model name its own call,
 * and in particular nothing that feeds model-authored text back in as a prompt:
 * a dashboard can be built out of search results, and a button that re-submits
 * what those results said is an injection with a click on it.
 */
export const actions = new ActionRegistry();

actions.register({
  type: "life.addSchedule",
  schema: {
    type: "object",
    properties: {
      title: { type: "string" },
      start_at: { type: "string" },
      location: { type: "string" },
      participants: { type: "array", items: { type: "string" } },
    },
    required: ["title", "start_at"],
  },
  run: async (p: {
    title: string;
    start_at: string;
    location?: string;
    participants?: string[];
  }) => unwrap(await AddSchedule(p.title, p.start_at, p.location ?? "", p.participants ?? [])),
});

actions.register({
  type: "life.addRecord",
  schema: {
    type: "object",
    properties: {
      type: { type: "string" },
      title: { type: "string" },
      body: { type: "string" },
      tags: { type: "array", items: { type: "string" } },
      project: { type: "string" },
    },
    required: ["type", "body"],
  },
  run: async (p: {
    type: string;
    title?: string;
    body: string;
    tags?: string[];
    project?: string;
  }) =>
    unwrap(
      await AddRecord(p.type, p.title ?? "", p.body, p.tags ?? [], p.project ?? ""),
    ),
});

/**
 * The App methods answer `{ok, data}` rather than throwing, because the same
 * shape crosses the Wails bridge and the serve-mode JSON RPC. The action
 * runtime reports a rejected promise as a failed action, so a refusal has to
 * become one here or the button quietly reports success.
 */
function unwrap(res: Record<string, any>): unknown {
  if (!res?.ok) throw new Error(String(res?.error ?? "action failed"));
  return res.data;
}

export const actionRuntime = new ActionRuntime({ registry: actions });

/**
 * Plugins must be a stable reference — rebuilding the array on every render
 * would tear down and remount every diagram and chart mid-stream.
 *
 * `highlight` is deliberately absent: it claims the same `code` node as the
 * host renderer below, and copying a snippet matters more here than colouring
 * it — it also kept a megabyte of Shiki language grammars in the bundle.
 *
 * `bigscreen` and `ui` are how a dashboard arrives: the model lays out panels
 * and interfaces, in a normal reply, with no dedicated page behind it. Neither
 * one loosens the rule that the renderer's only input is model output — a wall
 * of counting numbers is presentation, and the figures on it are real only
 * because the model read them off a tool result first. Both prompt specs say
 * so; `theme` is left to the renderer context so a dark transcript stays dark.
 */
export const plugins = [
  primitives(), // list / table / key-value / layout
  katex(),      // $…$ and $$…$$
  mermaid(),    // ```mermaid diagrams
  chart({ interactive: true }), // ```chart ECharts options
  citation(),   // ```sources
  bigscreen(),  // ```bigscreen KPI / gauge / rank / 3D / globe walls
  ui({ registry, actionRuntime }), // ```ui declarative interfaces
];

/**
 * Host renderers, which win over any plugin claiming the same node type.
 *
 * Code blocks are ours so they keep the copy button: the plugin that renders
 * them has no idea this is a desktop app where copying a snippet is the whole
 * point of showing it.
 */
export const nodeRenderers: Record<string, NodeRenderer> = {
  code: (node) => ({
    kind: "mount",
    mount: (el) => {
      const root = createRoot(el);
      root.render(
        <CodeBlock code={node.content ?? ""} language={node.attrs?.lang || "text"} />,
      );
      // Unmount on the next tick: React refuses to tear a root down while it is
      // still rendering the tree that owns this element.
      return () => queueMicrotask(() => root.unmount());
    },
  }),
};

/**
 * The generation rules for everything registered above, to be appended to the
 * agent's own system prompt. Built from the same registry and plugins the
 * renderer uses, so the two can never drift apart.
 */
export function uiRules(): string {
  return buildSystemPrompt({ registry, plugins, locale: APP_LOCALE });
}

/**
 * A block written on one line is not a block.
 *
 * Models regularly emit ```list {"items":[…]}``` — opening fence, payload and
 * closing fence all on a single line. That is not a fenced code block in
 * CommonMark: an info string may not contain backticks, so the line parses as
 * an inline code span and the reader gets raw JSON running through the middle
 * of a sentence instead of a list.
 *
 * Prompt wording moves how often this happens without reaching zero, and the
 * failure is loud and ugly every time. So it is repaired here, where it is a
 * one-line rewrite into the shape the parser is looking for.
 *
 * Deliberately narrow: the whole line must be fence, language, payload, fence.
 * A partial line mid-stream has no closing fence and is left alone until it
 * arrives, and ordinary inline code never starts a line with three backticks.
 */
const ONE_LINE_FENCE = /^([ \t]*)```([A-Za-z][\w-]*)[ \t]+(\S.*?)[ \t]*```[ \t]*$/gm;

export function normalizeOneLineBlocks(text: string): string {
  if (!text.includes("```")) return text;
  return text.replace(
    ONE_LINE_FENCE,
    (_m, indent: string, lang: string, payload: string) =>
      `${indent}\`\`\`${lang}\n${indent}${payload}\n${indent}\`\`\``,
  );
}

/**
 * The current colour scheme, tracked from <html data-theme>. Charts and
 * diagrams pick their own colours and cannot read the page, so the renderer has
 * to be told — otherwise a dark transcript gets white plot areas.
 */
export function useThemeName(): string {
  const read = () => document.documentElement.dataset.theme || "dark";
  const [theme, setTheme] = useState(read);

  useEffect(() => {
    const observer = new MutationObserver(() => setTheme(read()));
    observer.observe(document.documentElement, {
      attributes: true,
      attributeFilter: ["data-theme"],
    });
    return () => observer.disconnect();
  }, []);

  return theme;
}
