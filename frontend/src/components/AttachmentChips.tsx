import React from "react";
import { FileIcon, XIcon } from "lucide-react";

/** The pending-attachment row shared by the Chat and Agent composers. */
export default function AttachmentChips({
  paths,
  onRemove,
}: {
  paths: string[];
  onRemove: (p: string) => void;
}) {
  if (paths.length === 0) return null;
  return (
    <div style={{ display: "flex", flexWrap: "wrap", gap: 6, padding: "0 2px 8px" }}>
      {paths.map((p) => (
        <span
          key={p}
          title={p}
          style={{
            display: "inline-flex", alignItems: "center", gap: 6,
            fontSize: 12, padding: "3px 8px", borderRadius: 8,
            background: "var(--accent-soft)", color: "var(--accent-hover)",
            border: "1px solid var(--accent-border)", maxWidth: 260,
          }}
        >
          <FileIcon className="size-3.5" style={{ flex: "none" }} />
          <span style={{ overflow: "hidden", textOverflow: "ellipsis", whiteSpace: "nowrap" }}>
            {p.replace(/^uploads\//, "")}
          </span>
          <XIcon
            className="size-3.5"
            style={{ cursor: "pointer", flex: "none" }}
            onClick={() => onRemove(p)}
          />
        </span>
      ))}
    </div>
  );
}
