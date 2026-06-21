import { cn } from "@/lib/utils";
import type { ComponentProps } from "react";
import { useEffect, useRef } from "react";

export type ConversationProps = ComponentProps<"div">;

/**
 * Conversation is a scrollable log region. (AI Elements normally wraps
 * use-stick-to-bottom; here we keep the same API but drive auto-scroll
 * ourselves so we stay decoupled from the AI SDK — our messages come from
 * the Wails-backed useChat hook, not an HTTP stream.)
 */
export const Conversation = ({ className, ...props }: ConversationProps) => (
  <div
    className={cn("relative flex-1 overflow-y-auto", className)}
    role="log"
    {...props}
  />
);

export type ConversationContentProps = ComponentProps<"div"> & {
  /** Scroll to bottom whenever this value changes (e.g. messages.length). */
  autoScrollKey?: unknown;
};

export const ConversationContent = ({
  className,
  autoScrollKey,
  children,
  ...props
}: ConversationContentProps) => {
  const endRef = useRef<HTMLDivElement>(null);
  useEffect(() => {
    endRef.current?.scrollIntoView({ behavior: "smooth", block: "end" });
  }, [autoScrollKey]);
  return (
    <div className={cn("flex flex-col gap-6 p-4", className)} {...props}>
      {children}
      <div ref={endRef} />
    </div>
  );
};

export type ConversationEmptyStateProps = ComponentProps<"div"> & {
  title?: string;
  description?: string;
  icon?: React.ReactNode;
};

export const ConversationEmptyState = ({
  className,
  title = "No messages yet",
  description = "Start a conversation to see messages here",
  icon,
  children,
  ...props
}: ConversationEmptyStateProps) => (
  <div
    className={cn(
      "flex size-full flex-col items-center justify-center gap-3 p-8 text-center",
      className,
    )}
    {...props}
  >
    {children ?? (
      <>
        {icon && <div className="text-muted-foreground">{icon}</div>}
        <div className="space-y-1">
          <h3 className="font-medium text-sm">{title}</h3>
          {description && (
            <p className="text-muted-foreground text-sm">{description}</p>
          )}
        </div>
      </>
    )}
  </div>
);
