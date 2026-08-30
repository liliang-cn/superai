import { cn } from "@/lib/utils";
import { useImeGuard } from "@/lib/ime";
import { Button } from "@/components/ui/button";
import { Loader2Icon, SendIcon, SquareIcon } from "lucide-react";
import type {
  ComponentProps,
  FormEvent,
  FormEventHandler,
  KeyboardEvent,
} from "react";

export type PromptInputMessage = {
  text: string;
};

export type PromptInputProps = Omit<ComponentProps<"form">, "onSubmit"> & {
  onSubmit?: (
    message: PromptInputMessage,
    event: FormEvent<HTMLFormElement>,
  ) => void;
};

export const PromptInput = ({
  className,
  onSubmit,
  children,
  ...props
}: PromptInputProps) => {
  const handleSubmit: FormEventHandler<HTMLFormElement> = (event) => {
    event.preventDefault();
    if (!onSubmit) return;
    const form = event.currentTarget;
    const textarea = form.querySelector("textarea");
    const text = textarea ? (textarea as HTMLTextAreaElement).value : "";
    onSubmit({ text }, event);
  };
  return (
    <form
      className={cn(
        "w-full divide-y divide-border overflow-hidden rounded-xl border border-border bg-background shadow-sm",
        className,
      )}
      onSubmit={handleSubmit}
      {...props}
    >
      {children}
    </form>
  );
};

export type PromptInputTextareaProps = ComponentProps<"textarea">;

export const PromptInputTextarea = ({
  className,
  onKeyDown,
  onCompositionStart,
  onCompositionEnd,
  placeholder = "Send a message…",
  ...props
}: PromptInputTextareaProps) => {
  // nativeEvent.isComposing alone is not enough in WKWebView, which is what
  // Wails renders in: the Enter that accepts a candidate can arrive after
  // compositionend, and a half-typed sentence gets sent. See lib/ime.ts.
  const ime = useImeGuard();
  const handleKeyDown = (e: KeyboardEvent<HTMLTextAreaElement>) => {
    if (e.key === "Enter" && !e.shiftKey && !ime.composing(e)) {
      e.preventDefault();
      e.currentTarget.form?.requestSubmit();
    }
    onKeyDown?.(e);
  };
  return (
    <textarea
      className={cn(
        "w-full resize-none bg-transparent px-4 py-3 text-sm outline-none",
        "max-h-48 min-h-[44px] field-sizing-content",
        "placeholder:text-muted-foreground disabled:opacity-60",
        className,
      )}
      name="message"
      onKeyDown={handleKeyDown}
      onCompositionStart={(e) => {
        ime.handlers.onCompositionStart(e);
        onCompositionStart?.(e);
      }}
      onCompositionEnd={(e) => {
        ime.handlers.onCompositionEnd(e);
        onCompositionEnd?.(e);
      }}
      placeholder={placeholder}
      {...props}
    />
  );
};

export type PromptInputToolbarProps = ComponentProps<"div">;

export const PromptInputToolbar = ({
  className,
  ...props
}: PromptInputToolbarProps) => (
  <div
    className={cn("flex items-center justify-between gap-2 p-2", className)}
    {...props}
  />
);

export type PromptInputToolsProps = ComponentProps<"div">;

export const PromptInputTools = ({
  className,
  ...props
}: PromptInputToolsProps) => (
  <div className={cn("flex items-center gap-1", className)} {...props} />
);

export type PromptInputSubmitProps = ComponentProps<typeof Button> & {
  status?: "ready" | "submitted" | "streaming" | "error";
};

export const PromptInputSubmit = ({
  className,
  status = "ready",
  children,
  size = "icon",
  ...props
}: PromptInputSubmitProps) => {
  let icon = <SendIcon className="size-4" />;
  if (status === "submitted" || status === "streaming") {
    icon = <Loader2Icon className="size-4 animate-spin" />;
  } else if (status === "error") {
    icon = <SquareIcon className="size-4" />;
  }
  return (
    <Button
      className={cn("rounded-lg", className)}
      size={size}
      type="submit"
      {...props}
    >
      {children ?? icon}
    </Button>
  );
};
