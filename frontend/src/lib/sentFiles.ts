import { IMAGE_RE } from "./useAttachments";

/**
 * A sent message split back into what the person typed and the files that
 * went with it. useAttachments.build writes the files as reference blocks
 * ahead of the words, for the model; a bubble showing those blocks as text
 * read like a log line, and one showing only the words lost the files.
 */
export function splitSent(content: string): { text: string; files: string[]; images: string[] } {
  const files: string[] = [];
  const images: string[] = [];
  const rest: string[] = [];
  let into: string[] | null = null;
  for (const line of content.split("\n")) {
    if (line.startsWith("[文档附件")) { into = files; continue; }
    if (line.startsWith("[图片附件")) { into = images; continue; }
    const item = /^- (\S.*)$/.exec(line);
    if (into && item) { into.push(item[1].trim()); continue; }
    if (into && line.trim() === "") { into = null; continue; }
    into = null;
    rest.push(line);
  }
  // A path the block put under the wrong heading still shows as what it is.
  for (const p of [...files]) if (IMAGE_RE.test(p)) { files.splice(files.indexOf(p), 1); images.push(p); }
  return { text: rest.join("\n").trim(), files, images };
}
