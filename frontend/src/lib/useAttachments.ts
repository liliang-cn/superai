import { ClipboardEvent, useCallback, useEffect, useState } from "react";
import { OnFileDrop, OnFileDropOff } from "../../wailsjs/runtime";
import { ImportFiles, ImportPastedFile, PickFiles } from "../../wailsjs/go/main/App";
import { toast } from "./toasts";

const IMAGE_RE = /\.(png|jpe?g|gif|webp|bmp|tiff?)$/i;

/** The backend's own ceiling on one pasted file, kept in step with app_files.go. */
const MAX_PASTE_BYTES = 16 << 20;

export interface Attachments {
  paths: string[];
  importing: boolean;
  pick: () => void;
  /** Composer paste handler: clipboard files become attachments. */
  paste: (e: ClipboardEvent) => void;
  remove: (p: string) => void;
  clear: () => void;
  /**
   * Turn the typed text plus the pending attachments into what actually gets
   * sent: images ride along as multimodal input, documents are referenced by
   * their workspace path for read_document to open.
   */
  build: (text: string) => { payload: string; images: string[] };
}

/**
 * Attachment handling shared by the Chat and Agent views: native OS file drop,
 * the file picker, and the payload both views send.
 */
export function useAttachments(): Attachments {
  const [paths, setPaths] = useState<string[]>([]);
  const [importing, setImporting] = useState(false);

  const add = useCallback((rels: string[] | null) => {
    if (!rels || rels.length === 0) return;
    setPaths((prev) => Array.from(new Set([...prev, ...rels])));
  }, []);

  // Native OS file drop -> copy into the agent workspace -> attach the path.
  useEffect(() => {
    OnFileDrop((_x, _y, dropped) => {
      if (!dropped || dropped.length === 0) return;
      setImporting(true);
      ImportFiles(dropped)
        .then(add)
        .catch(() => {})
        .finally(() => setImporting(false));
    }, true);
    return () => OnFileDropOff();
  }, [add]);

  const pick = useCallback(() => {
    setImporting(true);
    PickFiles()
      .then(add)
      .catch(() => {})
      .finally(() => setImporting(false));
  }, [add]);

  /**
   * Paste.
   *
   * A drop and the picker both hand over a path; the clipboard hands over
   * bytes, so a screenshot goes to the backend as a data URL and comes back as
   * a workspace path like any other attachment.
   *
   * Only a paste that carries no text is taken. Copying a range of cells out
   * of a spreadsheet puts both text and a picture of it on the clipboard, and
   * swallowing that paste to attach the picture would take away the thing the
   * person was actually pasting. No text means the clipboard holds a file and
   * nothing else — a screenshot, or something copied in Finder.
   */
  const paste = useCallback(
    (e: ClipboardEvent) => {
      const cb = e.clipboardData;
      const files = Array.from(cb?.files ?? []);
      if (files.length === 0 || (cb?.getData("text/plain") ?? "") !== "") return;
      e.preventDefault();
      setImporting(true);
      // One at a time: two files pasted together should keep the order they
      // were copied in, and the chips read better for it.
      (async () => {
        const rels: string[] = [];
        for (const f of files) {
          if (f.size > MAX_PASTE_BYTES) {
            toast.warn(`${f.name || "The pasted file"} is too big to attach (over ${MAX_PASTE_BYTES >> 20}MB).`);
            continue;
          }
          try {
            rels.push(await ImportPastedFile(f.name, await readDataURL(f)));
          } catch (err) {
            toast.error(`Could not attach the pasted file: ${String(err)}`);
          }
        }
        add(rels);
      })().finally(() => setImporting(false));
    },
    [add],
  );

  const remove = useCallback((p: string) => {
    setPaths((prev) => prev.filter((x) => x !== p));
  }, []);

  const clear = useCallback(() => setPaths([]), []);

  const build = useCallback(
    (text: string) => {
      const trimmed = text.trim();
      if (paths.length === 0) return { payload: trimmed, images: [] };

      const images = paths.filter((p) => IMAGE_RE.test(p));
      const docs = paths.filter((p) => !IMAGE_RE.test(p));
      const refs: string[] = [];
      if (docs.length)
        refs.push(`[文档附件，可用 read_document 工具读取]\n${docs.map((p) => `- ${p}`).join("\n")}`);
      if (images.length)
        refs.push(`[图片附件已随消息发送给视觉模型，可直接查看]\n${images.map((p) => `- ${p}`).join("\n")}`);

      const instruction =
        trimmed || (images.length && !docs.length ? "描述这张图片。" : "读取这些文件并总结要点。");
      return { payload: `${refs.join("\n\n")}\n\n${instruction}`, images };
    },
    [paths],
  );

  return { paths, importing, pick, paste, remove, clear, build };
}

/** One clipboard file as a data URL, which is what ImportPastedFile takes. */
function readDataURL(file: File): Promise<string> {
  return new Promise((resolve, reject) => {
    const reader = new FileReader();
    reader.onerror = () => reject(reader.error ?? new Error("could not be read"));
    reader.onload = () => resolve(String(reader.result));
    reader.readAsDataURL(file);
  });
}
