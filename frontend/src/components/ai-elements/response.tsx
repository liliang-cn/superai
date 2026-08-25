import { cn } from "@/lib/utils";
import { memo } from "react";
import type { ComponentProps } from "react";
import { AIRenderer } from "@ai-gui/react";
import {
  APP_LOCALE,
  normalizeOneLineBlocks,
  nodeRenderers,
  plugins,
  registry,
  useThemeName,
} from "@/lib/aigui";

type ResponseProps = ComponentProps<"div"> & {
  children: string;
};

/**
 * Response renders assistant output through AIGUI: markdown streams in
 * progressively, while the richer blocks a model emits — tables, math, code,
 * mermaid diagrams, ECharts charts, source lists, and any card registered in
 * lib/aigui — render as real UI once they are complete.
 *
 * AIRenderer is controlled by `text`: handing it the full string on every
 * update lets it push just the delta, which is exactly the shape useChat keeps
 * a streaming message in.
 *
 * The text is repaired on the way in — see normalizeOneLineBlocks. Every place
 * assistant output is shown goes through here, so the repair happens once.
 */
export const Response = memo(
  ({ className, children, ...props }: ResponseProps) => {
    const theme = useThemeName();
    return (
      <div
        className={cn(
          "ai-response prose-sm max-w-none [&>*:first-child]:mt-0 [&>*:last-child]:mb-0",
          className,
        )}
        {...props}
      >
        <AIRenderer
          className="md"
          text={normalizeOneLineBlocks(children)}
          registry={registry}
          plugins={plugins}
          nodeRenderers={nodeRenderers}
          theme={theme}
          locale={APP_LOCALE}
        />
      </div>
    );
  },
  (prev, next) => prev.children === next.children,
);

Response.displayName = "Response";
