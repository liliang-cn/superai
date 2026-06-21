import { cn } from "@/lib/utils";
import { memo } from "react";
import type { ComponentProps } from "react";
import ReactMarkdown from "react-markdown";
import remarkGfm from "remark-gfm";
import { CodeBlock } from "./code-block";

type ResponseProps = ComponentProps<"div"> & {
  children: string;
};

const mdComponents = {
  code({ inline, className, children, ...props }: any) {
    if (inline) {
      return (
        <code
          className={cn(
            "rounded bg-muted px-1.5 py-0.5 font-mono text-[0.85em]",
            className,
          )}
          {...props}
        >
          {children}
        </code>
      );
    }
    const lang = (className || "").replace("language-", "");
    return (
      <CodeBlock
        code={String(children).replace(/\n$/, "")}
        language={lang || "text"}
      />
    );
  },
  // CodeBlock renders its own container; unwrap react-markdown's outer <pre>.
  pre({ children }: any) {
    return <>{children}</>;
  },
};

/**
 * Response renders streaming/assistant markdown. Handles incremental markdown
 * gracefully because react-markdown re-parses on every render. Uses our
 * existing react-markdown + remark-gfm rather than Streamdown to keep the
 * dependency surface small on the Wails/Vite build.
 */
export const Response = memo(
  ({ className, children, ...props }: ResponseProps) => (
    <div
      className={cn(
        "ai-response prose-sm max-w-none [&>*:first-child]:mt-0 [&>*:last-child]:mb-0",
        className,
      )}
      {...props}
    >
      <div className="md">
        <ReactMarkdown remarkPlugins={[remarkGfm]} components={mdComponents}>
          {children}
        </ReactMarkdown>
      </div>
    </div>
  ),
  (prev, next) => prev.children === next.children,
);

Response.displayName = "Response";
