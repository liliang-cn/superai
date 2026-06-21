import { cn } from "@/lib/utils";
import type { ComponentProps, HTMLAttributes } from "react";

export type MessageProps = HTMLAttributes<HTMLDivElement> & {
  from: "user" | "assistant" | "system";
};

export const Message = ({ className, from, ...props }: MessageProps) => (
  <div
    className={cn(
      "group flex w-full items-end justify-end gap-2 py-1",
      from === "user" ? "is-user" : "is-assistant flex-row-reverse justify-end",
      "[&>div]:max-w-[80%]",
      className,
    )}
    data-from={from}
    {...props}
  />
);

export type MessageContentProps = HTMLAttributes<HTMLDivElement> & {
  variant?: "contained" | "flat";
};

export const MessageContent = ({
  children,
  className,
  variant = "contained",
  ...props
}: MessageContentProps) => (
  <div
    className={cn(
      "flex flex-col gap-2 overflow-hidden text-sm",
      variant === "contained" && [
        "rounded-lg px-4 py-3",
        "group-[.is-user]:bg-primary group-[.is-user]:text-primary-foreground",
        "group-[.is-assistant]:bg-secondary group-[.is-assistant]:text-foreground",
      ],
      variant === "flat" && [
        "group-[.is-user]:max-w-[80%] group-[.is-user]:rounded-lg group-[.is-user]:bg-secondary group-[.is-user]:px-4 group-[.is-user]:py-3 group-[.is-user]:text-foreground",
        "group-[.is-assistant]:text-foreground",
      ],
      className,
    )}
    {...props}
  >
    {children}
  </div>
);

export type MessageAvatarProps = ComponentProps<"div"> & {
  name?: string;
};

export const MessageAvatar = ({
  name,
  className,
  ...props
}: MessageAvatarProps) => (
  <div
    className={cn(
      "flex size-8 shrink-0 select-none items-center justify-center rounded-full bg-muted text-muted-foreground text-xs font-semibold",
      className,
    )}
    {...props}
  >
    {name}
  </div>
);
