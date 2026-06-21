import { cn } from "@/lib/utils";
import { Badge } from "@/components/ui/badge";
import {
  Collapsible,
  CollapsibleContent,
  CollapsibleTrigger,
} from "@/components/ui/collapsible";
import {
  CheckCircle2Icon,
  ChevronDownIcon,
  CircleIcon,
  ClockIcon,
  XCircleIcon,
  WrenchIcon,
} from "lucide-react";
import type { ComponentProps, ReactNode } from "react";

export type ToolState =
  | "input-streaming"
  | "input-available"
  | "output-available"
  | "output-error";

export type ToolProps = ComponentProps<typeof Collapsible>;

export const Tool = ({ className, ...props }: ToolProps) => (
  <Collapsible
    className={cn("not-prose w-full rounded-md border border-border", className)}
    {...props}
  />
);

const statusBadge: Record<ToolState, { label: string; icon: ReactNode }> = {
  "input-streaming": {
    label: "Pending",
    icon: <CircleIcon className="size-3.5 text-muted-foreground" />,
  },
  "input-available": {
    label: "Running",
    icon: <ClockIcon className="size-3.5 animate-pulse text-amber-500" />,
  },
  "output-available": {
    label: "Completed",
    icon: <CheckCircle2Icon className="size-3.5 text-green-500" />,
  },
  "output-error": {
    label: "Error",
    icon: <XCircleIcon className="size-3.5 text-destructive" />,
  },
};

export type ToolHeaderProps = {
  type: string;
  state: ToolState;
  className?: string;
};

export const ToolHeader = ({ className, type, state }: ToolHeaderProps) => {
  const s = statusBadge[state] ?? statusBadge["input-available"];
  return (
    <CollapsibleTrigger
      className={cn(
        "flex w-full items-center justify-between gap-3 p-2.5 text-left",
        className,
      )}
    >
      <div className="flex min-w-0 items-center gap-2">
        <WrenchIcon className="size-3.5 shrink-0 text-muted-foreground" />
        <span className="truncate font-mono text-xs font-medium text-foreground">
          {type}
        </span>
        <Badge className="gap-1.5 rounded-full" variant="secondary">
          {s.icon}
          {s.label}
        </Badge>
      </div>
      <ChevronDownIcon className="size-4 shrink-0 text-muted-foreground transition-transform group-data-[state=open]:rotate-180" />
    </CollapsibleTrigger>
  );
};

export type ToolContentProps = ComponentProps<typeof CollapsibleContent>;

export const ToolContent = ({ className, ...props }: ToolContentProps) => (
  <CollapsibleContent
    className={cn(
      "overflow-hidden border-t border-border text-popover-foreground",
      "data-[state=closed]:animate-out data-[state=open]:animate-in",
      className,
    )}
    {...props}
  />
);

export type ToolInputProps = {
  input: unknown;
  className?: string;
};

export const ToolInput = ({ input, className }: ToolInputProps) => (
  <div className={cn("space-y-1 p-3", className)}>
    <h4 className="text-xs font-medium uppercase tracking-wide text-muted-foreground">
      Parameters
    </h4>
    <pre className="overflow-x-auto rounded bg-muted/50 p-2 text-[0.78em] leading-relaxed">
      <code>{format(input)}</code>
    </pre>
  </div>
);

export type ToolOutputProps = {
  output?: ReactNode;
  errorText?: string;
  className?: string;
};

export const ToolOutput = ({ output, errorText, className }: ToolOutputProps) => {
  if (output == null && !errorText) return null;
  return (
    <div className={cn("space-y-1 border-t border-border p-3", className)}>
      <h4 className="text-xs font-medium uppercase tracking-wide text-muted-foreground">
        {errorText ? "Error" : "Result"}
      </h4>
      <div
        className={cn(
          "overflow-x-auto rounded p-2 text-[0.78em] leading-relaxed",
          errorText
            ? "bg-destructive/10 text-destructive"
            : "bg-muted/50",
        )}
      >
        {errorText ? (
          <pre>
            <code>{errorText}</code>
          </pre>
        ) : (
          output
        )}
      </div>
    </div>
  );
};

function format(value: unknown): string {
  if (typeof value === "string") return value;
  try {
    return JSON.stringify(value, null, 2);
  } catch {
    return String(value);
  }
}
