import { useState } from "react";
import { createPortal } from "react-dom";
import { CheckIcon, CopyIcon } from "lucide-react";
import { copyText } from "@/lib/format";
import { useI18n } from "@/lib/i18n";
import RunTracePanel from "./RunTracePanel";

/**
 * An answer's trace id: the flow every run, tool call, worker turn and
 * CortexDB call made for it shares. The id opens the flow's timeline; the
 * icon beside it copies the whole id, for `agentgo-trace --trace <id>` or a
 * grep through the logs.
 */
export default function TraceChip({ traceId, session }: { traceId: string; session?: string }) {
  const { t } = useI18n();
  const [open, setOpen] = useState(false);
  const [copied, setCopied] = useState(false);
  if (!traceId) return null;
  const copy = async () => {
    if (await copyText(traceId)) {
      setCopied(true);
      window.setTimeout(() => setCopied(false), 1200);
    }
  };
  return (
    <span className="trace-chip" data-trace-id={traceId}>
      <button type="button" className="trace-chip-id" title={`${t("Trace")} ${traceId}`} onClick={() => setOpen(true)}>
        {traceId.slice(0, 8)}
      </button>
      <button type="button" className="trace-chip-copy" title={copied ? t("Copied") : t("Copy")} aria-label={t("Copy")} onClick={copy}>
        {copied ? <CheckIcon size={10} /> : <CopyIcon size={10} />}
      </button>
      {open && createPortal(<RunTracePanel taskId={session ?? ""} flowId={traceId} onClose={() => setOpen(false)} />, document.body)}
    </span>
  );
}
