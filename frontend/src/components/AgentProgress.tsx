import { useEffect, useState } from "react";
import {
  ActivityIcon,
  BrainIcon,
  ChevronDownIcon,
  Loader2Icon,
  WrenchIcon,
} from "lucide-react";
import { cn } from "@/lib/utils";
import {
  Collapsible,
  CollapsibleContent,
  CollapsibleTrigger,
} from "@/components/ui/collapsible";
import { ProgressStep } from "@/lib/types";

function fmtDuration(ms: number): string {
  const s = Math.max(0, Math.round(ms / 1000));
  if (s < 60) return `${s}s`;
  return `${Math.floor(s / 60)}m ${s % 60}s`;
}

/** Ticks while an ask is running so the header shows how long it has been at it. */
function useElapsed(startedAt: number | undefined, running: boolean): number {
  const [now, setNow] = useState(() => Date.now());
  useEffect(() => {
    if (!running) return;
    setNow(Date.now());
    const t = setInterval(() => setNow(Date.now()), 1000);
    return () => clearInterval(t);
  }, [running]);
  return startedAt ? now - startedAt : 0;
}

function StepIcon({ kind }: { kind: ProgressStep["kind"] }) {
  if (kind === "tool") return <WrenchIcon className="mt-0.5 size-3 shrink-0" />;
  if (kind === "state") return <ActivityIcon className="mt-0.5 size-3 shrink-0" />;
  return <span className="mt-[7px] size-1 shrink-0 rounded-full bg-current opacity-60" />;
}

/**
 * The agent's own account of a turn, sitting at the top of the bubble it belongs
 * to: what it is thinking, which tool it reached for, and which phase the
 * workflow is in. It lives in the bubble rather than in a global status line
 * because several asks can be running at once and a shared line could not say
 * which one it was talking about — and because the tool panel next to it is
 * collapsible, so a turn that writes and runs code was otherwise silent for its
 * whole 20 seconds.
 *
 * Collapsed it is one line — the newest thing that happened. Opened it is the
 * whole run.
 */
export default function AgentProgress({
  steps,
  running,
  startedAt,
  finishedAt,
}: {
  steps: ProgressStep[];
  running: boolean;
  startedAt?: number;
  finishedAt?: number;
}) {
  const live = useElapsed(startedAt, running);
  const elapsed = running ? live : Math.max(0, (finishedAt || 0) - (startedAt || 0));
  const duration = startedAt ? fmtDuration(elapsed) : "";
  const latest = steps[steps.length - 1];

  return (
    <Collapsible className="group not-prose w-full">
      <CollapsibleTrigger
        className={cn(
          "flex w-full items-center gap-1.5 text-left text-xs",
          "text-muted-foreground hover:text-foreground",
        )}
      >
        {running ? (
          <Loader2Icon className="size-3.5 shrink-0 animate-spin" />
        ) : (
          <BrainIcon className="size-3.5 shrink-0" />
        )}
        <span className="min-w-0 flex-1 truncate" aria-live="polite">
          {running
            ? latest?.text || "Working…"
            : `Worked for ${duration} · ${steps.length} step${steps.length === 1 ? "" : "s"}`}
        </span>
        {running && duration && (
          <span className="shrink-0 tabular-nums opacity-70">{duration}</span>
        )}
        <ChevronDownIcon className="size-3.5 shrink-0 transition-transform group-data-[state=open]:rotate-180" />
      </CollapsibleTrigger>
      <CollapsibleContent className="mt-1.5 space-y-1 border-l border-border pl-3 text-xs text-muted-foreground">
        {steps.map((s) => (
          <div key={s.id} className="flex items-start gap-1.5">
            <StepIcon kind={s.kind} />
            <span className={cn("min-w-0", s.kind === "thinking" && "italic")}>
              {s.kind === "tool" ? (
                <>
                  Calling <span className="font-mono">{s.tool}</span>
                </>
              ) : (
                s.text
              )}
            </span>
          </div>
        ))}
      </CollapsibleContent>
    </Collapsible>
  );
}
