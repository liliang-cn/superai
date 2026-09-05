import { cn } from "@/lib/utils";
import type { ComponentProps } from "react";
import { createContext, useCallback, useContext, useEffect, useRef, useState } from "react";
import { ChevronsDownIcon, ChevronsUpIcon } from "lucide-react";

/**
 * The scroll box a conversation lives in, and the two ways out of it.
 *
 * A long transcript is a place you get lost in: the answer you want is either
 * at the very top (what did I ask?) or the very bottom (what did it say?), and
 * everything between them is a lot of dragging. So the box carries a jump to
 * each end, shown only while there is somewhere to jump to.
 *
 * The buttons cannot live inside the scrolling element — they would scroll
 * away with the content — so this renders a wrapper and puts them over it.
 * The className the caller passes still lands on the scrolling element, which
 * is where `.transcript` belongs.
 */

/** How far from an end counts as "not there yet", in pixels. Below this the
 *  jump would move almost nothing and the button is just clutter. */
const jumpThreshold = 240;

const ScrollBox = createContext<React.RefObject<HTMLDivElement | null> | null>(null);

export type ConversationProps = ComponentProps<"div">;

export const Conversation = ({ className, children, ...props }: ConversationProps) => {
  const ref = useRef<HTMLDivElement>(null);
  const [reach, setReach] = useState({ up: false, down: false });

  const measure = useCallback(() => {
    const el = ref.current;
    if (!el) return;
    const fromBottom = el.scrollHeight - el.clientHeight - el.scrollTop;
    setReach({ up: el.scrollTop > jumpThreshold, down: fromBottom > jumpThreshold });
  }, []);

  useEffect(() => {
    const el = ref.current;
    if (!el) return;
    measure();
    el.addEventListener("scroll", measure, { passive: true });
    // The content grows while an answer streams, and a transcript that just
    // became scrollable has to offer the way back without waiting for a
    // scroll event that nobody is going to produce.
    const ro = new ResizeObserver(measure);
    ro.observe(el);
    for (const child of Array.from(el.children)) ro.observe(child);
    return () => {
      el.removeEventListener("scroll", measure);
      ro.disconnect();
    };
  }, [measure, children]);

  const jump = (to: "top" | "bottom") => {
    const el = ref.current;
    if (!el) return;
    el.scrollTo({ top: to === "top" ? 0 : el.scrollHeight, behavior: "smooth" });
  };

  return (
    <ScrollBox.Provider value={ref}>
      <div className="conv-wrap">
        <div ref={ref} className={cn("relative flex-1 overflow-y-auto", className)} role="log" {...props}>
          {children}
        </div>
        {(reach.up || reach.down) && (
          <div className="conv-jump">
            {reach.up && (
              <button
                type="button"
                className="conv-jump-btn"
                onClick={() => jump("top")}
                title="Jump to the start of this conversation"
                aria-label="Jump to the start of this conversation"
              >
                <ChevronsUpIcon className="size-4" />
              </button>
            )}
            {reach.down && (
              <button
                type="button"
                className="conv-jump-btn"
                onClick={() => jump("bottom")}
                title="Jump to the latest message"
                aria-label="Jump to the latest message"
              >
                <ChevronsDownIcon className="size-4" />
              </button>
            )}
          </div>
        )}
      </div>
    </ScrollBox.Provider>
  );
};

export type ConversationContentProps = ComponentProps<"div"> & {
  /** Scroll to bottom whenever this value changes (e.g. messages.length). */
  autoScrollKey?: unknown;
  /**
   * The conversation being shown. When this changes the jump to the bottom is
   * instant rather than animated: opening a hundred-turn transcript used to
   * smooth-scroll the whole way from the top, which reads as the app scrolling
   * away from you, and lands late enough that a slow block can move the target
   * out from under it.
   */
  jumpKey?: unknown;
};

export const ConversationContent = ({
  className,
  autoScrollKey,
  jumpKey,
  children,
  ...props
}: ConversationContentProps) => {
  const endRef = useRef<HTMLDivElement>(null);
  const contentRef = useRef<HTMLDivElement>(null);
  const box = useContext(ScrollBox);
  // Which conversation the last scroll was for. A change means "arrive", not
  // "follow along".
  const shown = useRef<unknown>(undefined);

  useEffect(() => {
    const arriving = shown.current !== jumpKey;
    shown.current = jumpKey;
    const el = box?.current;
    if (!arriving || !el) {
      endRef.current?.scrollIntoView({ behavior: "smooth", block: "end" });
      return;
    }

    // Arriving in a conversation: land at the bottom, instantly.
    //
    // One assignment is not enough. scrollHeight is whatever the browser can
    // measure at this instant, and a transcript is full of things that measure
    // late — markdown that reflows, code blocks that get highlighted, charts
    // that mount. Opening a 46-turn conversation landed 5,745px short of the
    // end for exactly that reason: the jump was right and the page then grew
    // underneath it. So hold the bottom while the content settles, and let go
    // the moment the reader takes over.
    let holding = true;
    const pin = () => {
      if (holding) el.scrollTop = el.scrollHeight;
    };
    pin();

    const release = () => {
      holding = false;
    };
    const ro = new ResizeObserver(pin);
    if (contentRef.current) ro.observe(contentRef.current);
    // Any deliberate move up is the reader saying they want to be elsewhere.
    el.addEventListener("wheel", release, { passive: true });
    el.addEventListener("touchstart", release, { passive: true });
    el.addEventListener("keydown", release);
    // A ceiling, so a conversation with something permanently animated in it
    // cannot pin the view forever.
    const giveUp = window.setTimeout(release, 3000);

    return () => {
      release();
      ro.disconnect();
      window.clearTimeout(giveUp);
      el.removeEventListener("wheel", release);
      el.removeEventListener("touchstart", release);
      el.removeEventListener("keydown", release);
    };
  }, [autoScrollKey, jumpKey, box]);

  return (
    <div ref={contentRef} className={cn("flex flex-col gap-6 p-4", className)} {...props}>
      {children}
      <div ref={endRef} />
    </div>
  );
};

export type ConversationEmptyStateProps = ComponentProps<"div"> & {
  title?: string;
  description?: string;
  icon?: React.ReactNode;
};

export const ConversationEmptyState = ({
  className,
  title = "No messages yet",
  description = "Start a conversation to see messages here",
  icon,
  children,
  ...props
}: ConversationEmptyStateProps) => (
  <div
    className={cn(
      "flex size-full flex-col items-center justify-center gap-3 p-8 text-center",
      className,
    )}
    {...props}
  >
    {icon && <div className="text-muted-foreground">{icon}</div>}
    <div className="space-y-1">
      <h3 className="font-medium text-sm">{title}</h3>
      {description && (
        <p className="text-muted-foreground text-sm">{description}</p>
      )}
    </div>
    {/* Children add to the empty state rather than replacing it: callers pass a
        conditional hint (`{notReady && …}`), and a false child used to swallow
        the icon and title with it, leaving the pane blank. */}
    {children}
  </div>
);
