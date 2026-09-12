import { cn } from "@/lib/utils";
import type { HTMLAttributes } from "react";

/**
 * One turn in the transcript, and the two very different shapes a turn takes.
 *
 * The sides are deliberately not symmetrical. What a person typed is a block:
 * short, addressed to someone, pinned to the right. What the assistant wrote is
 * a document — prose, code blocks, tables, whole dashboards — and a document
 * put inside a speech bubble is a document in a box too small for it. So the
 * reply gets no bubble at all: a copper rule down its left edge says who is
 * speaking, and the text sits straight on the paper.
 *
 * The Tailwind that used to draw both bubbles here is gone, along with the
 * avatars. Layout and colour live in styles.css (.msg-row / .msg-content),
 * where the rest of the transcript already is; this file only names the parts.
 */

export type MessageProps = HTMLAttributes<HTMLDivElement> & {
  from: "user" | "assistant" | "system";
};

export const Message = ({ className, from, ...props }: MessageProps) => (
  <div
    className={cn("msg-row", from === "user" ? "is-user" : "is-assistant", className)}
    data-from={from}
    {...props}
  />
);

export type MessageContentProps = HTMLAttributes<HTMLDivElement>;

export const MessageContent = ({ className, children, ...props }: MessageContentProps) => (
  <div className={cn("msg-content", className)} {...props}>
    {children}
  </div>
);

export type MessageBylineProps = {
  /** Who wrote this. Set in the display serif, in copper. */
  name: string;
  /** What the turn cost — "34s · 6 tools". Left off when nothing is known. */
  meta?: string;
};

/**
 * The signature above an answer.
 *
 * Without a bubble there is nothing in the shape of the block that says who is
 * talking, and the copper rule alone only says "not you". The name does the
 * rest, and the meta beside it is the one thing worth knowing about a reply
 * that has already finished: how long it took and how much work it was.
 */
export const MessageByline = ({ name, meta }: MessageBylineProps) => (
  <div className="msg-byline">
    <span className="msg-byline-name">{name}</span>
    {meta && <span className="msg-byline-meta">{meta}</span>}
  </div>
);
