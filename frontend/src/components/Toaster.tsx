import { AlertTriangleIcon, CheckIcon, InfoIcon, MessageSquareIcon, XCircleIcon, XIcon } from "lucide-react";
import { Toast, ToastLevel, dismiss, useToasts } from "../lib/toasts";

/**
 * Where a notice lands when the page is what you are looking at.
 *
 * Bottom left, because the scheduled-run toasts already own the bottom right
 * and those carry a whole answer with buttons on it — two stacks growing into
 * each other from one corner is how a failure ends up hidden behind a report.
 */

const ICONS: Record<ToastLevel, typeof InfoIcon> = {
  info: InfoIcon,
  success: CheckIcon,
  warn: AlertTriangleIcon,
  error: XCircleIcon,
};

function ToastRow({ toast, onOpen }: { toast: Toast; onOpen?: (session: string) => void }) {
  const Icon = ICONS[toast.level];
  const clickable = Boolean(toast.session && onOpen);
  return (
    <div
      className={`toast toast-${toast.level}${clickable ? " toast-clickable" : ""}`}
      role={toast.level === "error" ? "alert" : "status"}
      onClick={clickable ? () => onOpen!(toast.session!) : undefined}
    >
      <Icon size={14} className="toast-icon" />
      <div className="toast-text">
        {toast.title !== "" && <div className="toast-title">{toast.title}</div>}
        <div className="toast-message">{toast.message}</div>
        {toast.source && (
          <div className="toast-source">
            <MessageSquareIcon size={11} />
            {toast.source}
          </div>
        )}
      </div>
      <button
        className="toast-close"
        aria-label="Dismiss"
        onClick={(e) => {
          // The row itself may open a conversation.
          e.stopPropagation();
          dismiss(toast.id);
        }}
      >
        <XIcon size={13} />
      </button>
    </div>
  );
}

export function Toaster({ onOpenConversation }: { onOpenConversation?: (session: string) => void }) {
  const toasts = useToasts();
  if (toasts.length === 0) return null;
  return (
    <div className="toasts" aria-live="polite">
      {toasts.map((t) => (
        <ToastRow key={t.id} toast={t} onOpen={onOpenConversation} />
      ))}
    </div>
  );
}
