import { useEffect, useState } from "react";
import { createRoot } from "react-dom/client";
import { CardRegistry, buildSystemPrompt, type NodeRenderer } from "@ai-gui/core";
import { primitives } from "@ai-gui/plugin-primitives";
import { katex } from "@ai-gui/plugin-katex";
import { mermaid } from "@ai-gui/plugin-mermaid";
import { chart } from "@ai-gui/plugin-chart";
import { citation } from "@ai-gui/plugin-citation";
import { CodeBlock } from "@/components/ai-elements/code-block";

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
 * Plugins must be a stable reference — rebuilding the array on every render
 * would tear down and remount every diagram and chart mid-stream.
 *
 * `highlight` is deliberately absent: it claims the same `code` node as the
 * host renderer below, and copying a snippet matters more here than colouring
 * it — it also kept a megabyte of Shiki language grammars in the bundle.
 */
export const plugins = [
  primitives(), // list / table / key-value / layout
  katex(),      // $…$ and $$…$$
  mermaid(),    // ```mermaid diagrams
  chart({ interactive: true }), // ```chart ECharts options
  citation(),   // ```sources
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
