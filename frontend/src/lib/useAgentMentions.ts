import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { RemoteAgentNames } from "../../wailsjs/go/main/App";

/**
 * The @ menu in the composer.
 *
 * Typing @ offers the agents this app can reach, and picking one puts its name
 * in. Where the name lands decides what happens to the message — at the front
 * it is an address and the whole thing goes to that agent, anywhere else it is
 * a mention SuperAI reads — so the composer says which of the two is about to
 * happen rather than leaving it to be discovered on send. That rule lives in
 * Go (addressedTo in app_remote.go); this only has to describe it.
 *
 * The list is asked for once. It comes from settings, which change on a page
 * nobody is on while typing a message.
 */

export interface AgentInfo {
  name: string;
  about: string;
}

/** What the composer is in the middle of typing, if anything. */
interface Query {
  /** Text between the @ and the caret. */
  word: string;
  /** Where the @ is, so accepting can replace from there. */
  at: number;
}

export interface AgentMentions {
  /** Every reachable agent. Empty means the feature is off; draw nothing. */
  agents: AgentInfo[];
  /** The matches for what is being typed, or [] when the menu is closed. */
  matches: AgentInfo[];
  /** Which match is highlighted. */
  active: number;
  /** The agent this message would be addressed to, or "" for an ordinary turn. */
  addressee: string;
  /** Feed the composer's value and caret in after every change. */
  update: (value: string, caret: number) => void;
  /** Arrow keys, Enter, Tab and Escape while the menu is open. Returns true
   *  when it handled the key, so the composer knows not to also send. */
  onKeyDown: (e: React.KeyboardEvent<HTMLTextAreaElement>) => boolean;
  /** Accept a match, returning the text and caret the composer should take. */
  accept: (agent: AgentInfo) => { value: string; caret: number } | null;
  /** Shut the menu without choosing. */
  close: () => void;
}

/**
 * The name at the very start of a message, if it is a known agent.
 *
 * Deliberately a little stricter than the Go side rather than a little looser:
 * this only draws a hint, and a hint that says "goes to pi" when it will not is
 * worse than no hint. The separators match app_remote.go.
 */
function addresseeOf(value: string, names: Set<string>): string {
  const m = /^[ \t]*@([\p{L}\p{N}._-]+)(\s|[:,：，]|$)/u.exec(value);
  if (!m) return "";
  return names.has(m[1]) ? m[1] : "";
}

/** The @word the caret is sitting in, if any. */
function queryAt(value: string, caret: number): Query | null {
  const before = value.slice(0, caret);
  const at = before.lastIndexOf("@");
  if (at < 0) return null;
  // An @ only opens the menu at the start of a word: nobody means to mention
  // an agent in the middle of an email address.
  if (at > 0 && !/[\s(（[]/.test(before[at - 1])) return null;
  const word = before.slice(at + 1);
  // A space ends it. So does anything that cannot be in a name — the menu
  // should be gone by the time someone has typed a sentence past the @.
  if (!/^[\p{L}\p{N}._-]*$/u.test(word)) return null;
  return { word, at };
}

export function useAgentMentions(
  value: string,
  setValue: (v: string) => void,
): AgentMentions {
  const [agents, setAgents] = useState<AgentInfo[]>([]);
  const [query, setQuery] = useState<Query | null>(null);
  const [active, setActive] = useState(0);
  // The caret the last update reported, so accept() can find the word again
  // without the composer having to hand it over twice.
  const caretRef = useRef(0);

  useEffect(() => {
    let alive = true;
    RemoteAgentNames()
      .then((list) => {
        if (!alive) return;
        setAgents(
          (list ?? []).map((a) => ({ name: a.name ?? "", about: a.about ?? "" })).filter((a) => a.name),
        );
      })
      .catch(() => {
        // The feature is off, or the call failed. Either way there is no menu,
        // which is the same as there being no agents.
      });
    return () => {
      alive = false;
    };
  }, []);

  const names = useMemo(() => new Set(agents.map((a) => a.name)), [agents]);
  const addressee = useMemo(() => addresseeOf(value, names), [value, names]);

  const matches = useMemo(() => {
    if (!query || agents.length === 0) return [];
    const w = query.word.toLowerCase();
    const hits = agents.filter((a) => a.name.toLowerCase().startsWith(w));
    // An exact and only match is not a menu, it is a label over what has
    // already been typed.
    if (hits.length === 1 && hits[0].name.toLowerCase() === w) return [];
    return hits;
  }, [query, agents]);

  const update = useCallback(
    (next: string, caret: number) => {
      caretRef.current = caret;
      const q = queryAt(next, caret);
      setQuery(q);
      setActive(0);
    },
    [],
  );

  const close = useCallback(() => setQuery(null), []);

  const accept = useCallback(
    (agent: AgentInfo) => {
      if (!query) return null;
      const head = value.slice(0, query.at);
      const tail = value.slice(caretRef.current);
      // A trailing space, because a name is always followed by the question.
      const inserted = `@${agent.name} `;
      setQuery(null);
      const out = { value: head + inserted + tail, caret: query.at + inserted.length };
      setValue(out.value);
      return out;
    },
    [query, value, setValue],
  );

  const onKeyDown = useCallback(
    (e: React.KeyboardEvent<HTMLTextAreaElement>) => {
      if (matches.length === 0) return false;
      switch (e.key) {
        case "ArrowDown":
          e.preventDefault();
          setActive((i) => (i + 1) % matches.length);
          return true;
        case "ArrowUp":
          e.preventDefault();
          setActive((i) => (i - 1 + matches.length) % matches.length);
          return true;
        case "Enter":
        case "Tab":
          // Enter is the send key, so swallowing it here is the whole reason
          // this returns a boolean: picking a name must never also send.
          e.preventDefault();
          accept(matches[active]);
          return true;
        case "Escape":
          e.preventDefault();
          setQuery(null);
          return true;
        default:
          return false;
      }
    },
    [matches, active, accept],
  );

  return { agents, matches, active, addressee, update, onKeyDown, accept, close };
}
