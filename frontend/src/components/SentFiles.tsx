import { useEffect, useState } from "react";
import { FileIcon } from "lucide-react";
import { ReadWorkspaceFileDataURL } from "../../wailsjs/go/app/App";
import { splitSent } from "../lib/sentFiles";

// Pictures already shown once, by path: a bubble scrolled away and back,
// or the same file in two views, is not read from disk again.
const seen = new Map<string, string>();

function Thumb({ path }: { path: string }) {
  const [url, setUrl] = useState(seen.get(path) ?? "");
  useEffect(() => {
    if (url) return;
    let live = true;
    ReadWorkspaceFileDataURL(path).then((u) => {
      if (!live || !u) return;
      seen.set(path, u);
      setUrl(u);
    }).catch(() => {});
    return () => { live = false; };
  }, [path, url]);
  const name = path.replace(/^uploads\//, "");
  return url
    ? <img className="sent-thumb" src={url} alt={name} title={name} />
    : <span className="sent-chip" title={path}><FileIcon size={13} />{name}</span>;
}

/** The files a sent message carried: pictures as pictures, the rest by name. */
export default function SentFiles({ images, files }: { images: string[]; files: string[] }) {
  if (images.length === 0 && files.length === 0) return null;
  return (
    <div className="sent-files" data-testid="sent-files">
      {images.map((p) => <Thumb key={p} path={p} />)}
      {files.map((p) => (
        <span key={p} className="sent-chip" title={p}><FileIcon size={13} />{p.replace(/^uploads\//, "")}</span>
      ))}
    </div>
  );
}

/** A message the person sent: the files it carried, then their words. */
export function SentMessage({ content }: { content: string }) {
  const { text, images, files } = splitSent(content);
  return (
    <>
      <SentFiles images={images} files={files} />
      {text && <span style={{ whiteSpace: "pre-wrap" }}>{text}</span>}
    </>
  );
}
