import { cn } from "@/lib/utils";
import {
  Collapsible,
  CollapsibleContent,
  CollapsibleTrigger,
} from "@/components/ui/collapsible";
import { BrainIcon, ChevronDownIcon } from "lucide-react";
import type { ComponentProps } from "react";
import { Response } from "./response";

export type ReasoningProps = ComponentProps<typeof Collapsible>;

export const Reasoning = ({ className, ...props }: ReasoningProps) => (
  <Collapsible className={cn("not-prose w-full", className)} {...props} />
);

export type ReasoningTriggerProps = ComponentProps<
  typeof CollapsibleTrigger
> & {
  title?: string;
};

export const ReasoningTrigger = ({
  className,
  title = "Reasoning",
  children,
  ...props
}: ReasoningTriggerProps) => (
  <CollapsibleTrigger
    className={cn(
      "flex items-center gap-1.5 text-xs text-muted-foreground hover:text-foreground",
      className,
    )}
    {...props}
  >
    {children ?? (
      <>
        <BrainIcon className="size-3.5" />
        <span>{title}</span>
        <ChevronDownIcon className="size-3.5 transition-transform group-data-[state=open]:rotate-180" />
      </>
    )}
  </CollapsibleTrigger>
);

export type ReasoningContentProps = ComponentProps<
  typeof CollapsibleContent
> & {
  children: string;
};

export const ReasoningContent = ({
  className,
  children,
  ...props
}: ReasoningContentProps) => (
  <CollapsibleContent
    className={cn("mt-2 text-sm text-muted-foreground", className)}
    {...props}
  >
    <Response>{children}</Response>
  </CollapsibleContent>
);
