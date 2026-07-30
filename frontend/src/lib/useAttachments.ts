import { useCallback, useEffect, useState } from "react";
import { OnFileDrop, OnFileDropOff } from "../../wailsjs/runtime";
import { ImportFiles, PickFiles } from "../../wailsjs/go/main/App";

const IMAGE_RE = /\.(png|jpe?g|gif|webp|bmp|tiff?)$/i;

export interface Attachments {
  paths: string[];
  importing: boolean;
  pick: () => void;
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

  return { paths, importing, pick, remove, clear, build };
}
