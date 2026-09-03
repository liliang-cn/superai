/**
 * A block written on one line is not a block.
 *
 * Models regularly emit ```list {"items":[…]}``` — opening fence, payload and
 * closing fence all on a single line. That is not a fenced code block in
 * CommonMark: an info string may not contain backticks, so the line parses as
 * an inline code span and the reader gets raw JSON running through the middle
 * of a sentence instead of a list.
 *
 * Prompt wording moves how often this happens without reaching zero, and the
 * failure is loud and ugly every time. So it is repaired here, where it is a
 * one-line rewrite into the shape the parser is looking for.
 *
 * It lives in its own module, with no imports, because both renderers need it:
 * the chat window, and the avatar page — which is served on a different port
 * with no bindings, and so cannot pull in lib/aigui and the App methods its
 * actions are wired to.
 */

/** Deliberately narrow: the whole line must be fence, language, payload, fence.
 *  A partial line mid-stream has no closing fence and is left alone until it
 *  arrives, and ordinary inline code never starts a line with three backticks. */
const ONE_LINE_FENCE = /^([ \t]*)```([A-Za-z][\w-]*)[ \t]+(\S.*?)[ \t]*```[ \t]*$/gm;

export function normalizeOneLineBlocks(text: string): string {
  if (!text.includes("```")) return text;
  return text.replace(
    ONE_LINE_FENCE,
    (_m, indent: string, lang: string, payload: string) =>
      `${indent}\`\`\`${lang}\n${indent}${payload}\n${indent}\`\`\``,
  );
}
