import { cn } from "@/lib/utils";
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
  placeholder = "Send a message…",
  ...props
}: PromptInputTextareaProps) => {
  const handleKeyDown = (e: KeyboardEvent<HTMLTextAreaElement>) => {
    if (e.key === "Enter" && !e.shiftKey && !e.nativeEvent.isComposing) {
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
