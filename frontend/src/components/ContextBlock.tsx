import { BookOpenIcon, ChevronDownIcon } from "lucide-react";
import { cn } from "@/lib/utils";
import {
  Collapsible,
  CollapsibleContent,
  CollapsibleTrigger,
} from "@/components/ui/collapsible";
import { Response } from "@/components/ai-elements/response";

/**
 * The memory the agent was handed before answering.
 *
 * It reaches the model as a user message, and was therefore drawn as one: a wall
 * of "## Memory Index" in a chat bubble, attributed to a person who never typed
 * it. It is still worth being able to open — it is why the answer says what it
 * says — so it gets a line of its own, folded, and its markdown is rendered by
 * the same pipeline as an answer rather than dumped as plain text.
 *
 * Deliberately not a Message: it is not part of the conversation, and giving it a
 * bubble is what made it read as one.
 */
export default function ContextBlock({ content }: { content: string }) {
  return (
    <Collapsible className="group not-prose w-full py-1">
      <CollapsibleTrigger
        className={cn(
          "flex w-full items-center gap-1.5 text-left text-xs",
          "text-muted-foreground hover:text-foreground",
        )}
      >
        <BookOpenIcon className="size-3.5 shrink-0" />
        <span className="min-w-0 flex-1 truncate">Recalled from memory</span>
        <ChevronDownIcon className="size-3.5 shrink-0 transition-transform group-data-[state=open]:rotate-180" />
      </CollapsibleTrigger>
      <CollapsibleContent className="ctx-body mt-1.5 border-l border-border pl-3">
        <Response>{content}</Response>
      </CollapsibleContent>
    </Collapsible>
  );
}
