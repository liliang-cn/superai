import { cn } from "@/lib/utils";
import { CheckIcon, CopyIcon } from "lucide-react";
import type { ComponentProps } from "react";
import { useState } from "react";
import { copyText } from "@/lib/format";

export type CodeBlockProps = ComponentProps<"div"> & {
  code: string;
  language?: string;
  showLineNumbers?: boolean;
};

/**
 * CodeBlock renders a fenced code block with a language label + copy button.
 * Kept lightweight (no Shiki) so the Wails/Vite build stays fast; syntax
 * styling comes from the .md pre/code rules in styles.css.
 */
export const CodeBlock = ({
  code,
  language = "text",
  className,
  ...props
}: CodeBlockProps) => {
  const [copied, setCopied] = useState(false);
  const onCopy = async () => {
    if (await copyText(code)) {
      setCopied(true);
      setTimeout(() => setCopied(false), 1200);
    }
  };
  return (
    <div
      className={cn(
        "my-2 overflow-hidden rounded-md border border-border bg-muted/40",
        className,
      )}
      {...props}
    >
      <div className="flex items-center justify-between border-b border-border bg-muted/60 px-3 py-1.5">
        <span className="font-mono text-xs text-muted-foreground">
          {language}
        </span>
        <button
          type="button"
          onClick={onCopy}
          className="inline-flex items-center gap-1 rounded px-1.5 py-0.5 text-xs text-muted-foreground hover:bg-accent hover:text-accent-foreground"
        >
          {copied ? (
            <CheckIcon className="size-3" />
          ) : (
            <CopyIcon className="size-3" />
          )}
          {copied ? "copied" : "copy"}
        </button>
      </div>
      <pre className="overflow-x-auto p-3 text-[0.82em] leading-relaxed">
        <code className={`language-${language}`}>{code}</code>
      </pre>
    </div>
  );
};
