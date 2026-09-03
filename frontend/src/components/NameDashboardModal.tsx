import React, { useEffect, useRef, useState } from "react";

/**
 * Naming a dashboard, in the app's own dialog.
 *
 * This was window.prompt, which in a desktop app announces itself as
 * "127.0.0.1:43521 says" — the app admitting it is a web page. A native-looking
 * product should not ask a question in the browser's voice.
 *
 * It also could not be styled, could not show what the name was suggested from,
 * and on the desktop build sat outside the window's own theme entirely.
 */
export default function NameDashboardModal({
  suggested,
  prompt,
  onCancel,
  onSave,
}: {
  suggested: string;
  /** The question this dashboard will re-ask, shown so it is clear a refresh
   *  has something to run — and clear when it does not. */
  prompt: string;
  onCancel: () => void;
  onSave: (name: string) => void;
}) {
  const [name, setName] = useState(suggested);
  const inputRef = useRef<HTMLInputElement>(null);

  // Focused and selected on open: the suggestion is usually close enough to
  // keep, and always close enough that typing over it is the fast path.
  useEffect(() => {
    inputRef.current?.focus();
    inputRef.current?.select();
  }, []);

  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if (e.key === "Escape") onCancel();
    };
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, [onCancel]);

  const submit = () => {
    const trimmed = name.trim();
    if (trimmed) onSave(trimmed);
  };

  return (
    <div className="modal-overlay" onClick={onCancel}>
      <div className="modal nd-modal" onClick={(e) => e.stopPropagation()} role="dialog" aria-label="Name this dashboard">
        <div className="modal-head">
          <span className="modal-title">Save as dashboard</span>
          <button className="modal-close" onClick={onCancel} aria-label="Cancel">
            ×
          </button>
        </div>
        <div className="nd-body">
          <label className="nd-label" htmlFor="nd-name">
            Name
          </label>
          <input
            id="nd-name"
            ref={inputRef}
            className="input"
            value={name}
            onChange={(e) => setName(e.target.value)}
            onKeyDown={(e) => {
              if (e.key === "Enter") submit();
            }}
            placeholder="e.g. 我的美股收益"
          />
          {prompt ? (
            <>
              <div className="nd-label">Refreshes by re-asking</div>
              <div className="nd-prompt">{prompt}</div>
            </>
          ) : (
            <div className="nd-note">
              No question sits behind this reply, so the dashboard will keep what
              it has and cannot refresh itself.
            </div>
          )}
        </div>
        <div className="nd-foot">
          <button className="btn ghost" onClick={onCancel}>
            Cancel
          </button>
          <button className="btn" onClick={submit} disabled={!name.trim()}>
            Save
          </button>
        </div>
      </div>
    </div>
  );
}
