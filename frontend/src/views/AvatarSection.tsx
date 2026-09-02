import React, { useState } from "react";
import { ClipboardSetText } from "../../wailsjs/runtime";
import { EmitAvatarTest } from "../../wailsjs/go/main/App";
import { AppStatus } from "../lib/types";
import { openExternal } from "../lib/openExternal";

/**
 * The avatar bridge, as a section of Settings rather than a page.
 *
 * It is two URLs to copy and a row of test buttons — something you set up once
 * and then leave alone, which is what Settings is for. As a top-level entry it
 * asked to be visited, and there was nothing to visit it for.
 */

/**
 * The emotions, in the order the sprite sheet's rows are in.
 *
 * The buttons used to show an emoji, which was drawn by the operating system
 * and so looked like a different face here than in the avatar it was testing.
 * They show the sprite the renderer will actually draw, from the same sheet, so
 * what you press is what you get.
 */
const EMOTIONS = ["neutral", "happy", "sad", "thinking", "excited"];

/** One frame of the sheet: 20x24 pixels, drawn at 2x. */
const SPRITE_W = 20;
const SPRITE_H = 24;
const SPRITE_ZOOM = 2;
const SHEET_FRAMES = 4;

export default function AvatarSection({
  status,
}: {
  status: AppStatus | null;
}) {
  const port =
    status?.avatarPort && status.avatarPort > 0 ? status.avatarPort : 0;
  const base = port ? `http://127.0.0.1:${port}` : "http://127.0.0.1:<port>";
  const eventsUrl = `${base}/avatar/events`;
  const pageUrl = `${base}/avatar`;
  const [fired, setFired] = useState<string>("");
  const [copied, setCopied] = useState(false);

  const test = async (emotion: string) => {
    try {
      await EmitAvatarTest(emotion);
      setFired(emotion);
      setTimeout(() => setFired(""), 1200);
    } catch {
      /* ignore */
    }
  };

  const copy = () => {
    ClipboardSetText(eventsUrl).then(() => {
      setCopied(true);
      setTimeout(() => setCopied(false), 1200);
    });
  };

  return (
    <>
      <div className="card">
        <div className="card-title">Avatar bridge</div>
        <div className="card-desc">
          Streams emotions to an external 2D / 3D renderer over SSE. Renderers
          connect to the event URL; the page below is a reference implementation
          of one.
          {!port && " Set an avatar port under Runtime to activate it."}
        </div>
        <div className="url-box">
          <span className="url-label">GET</span>
          <span style={{ flex: 1 }}>{eventsUrl}</span>
          <button className="btn ghost sm" onClick={copy} disabled={!port}>
            {copied ? "Copied!" : "Copy"}
          </button>
        </div>
        <div className="url-box" style={{ marginTop: 8 }}>
          <span className="url-label">PAGE</span>
          <span style={{ flex: 1 }}>{pageUrl}</span>
          <button
            className="btn sm"
            onClick={() => port && openExternal(pageUrl)}
            disabled={!port}
          >
            Open ↗
          </button>
        </div>

        <div className="card-desc" style={{ marginTop: 14 }}>
          Emit a test emotion to check a renderer is receiving.
        </div>
        <div className="emotion-btns">
          {EMOTIONS.map((e, i) => (
            <button
              key={e}
              className="emotion-btn"
              onClick={() => test(e)}
              disabled={!port}
              style={
                fired === e
                  ? {
                      borderColor: "var(--accent)",
                      background: "var(--accent-soft)",
                    }
                  : undefined
              }
            >
              <span
                className="em-sprite"
                style={{
                  // The still frame of each row. Testing is about which face
                  // the renderer picks, so the button holds still and the
                  // avatar page is where the animation lives.
                  backgroundImage: `url(${base}/avatar/sprites/cat.png)`,
                  backgroundSize: `${SPRITE_W * SHEET_FRAMES * SPRITE_ZOOM}px ${SPRITE_H * EMOTIONS.length * SPRITE_ZOOM}px`,
                  backgroundPositionY: `${-i * SPRITE_H * SPRITE_ZOOM}px`,
                  width: SPRITE_W * SPRITE_ZOOM,
                  height: SPRITE_H * SPRITE_ZOOM,
                }}
              />
              <span className="em-label">{e}</span>
            </button>
          ))}
        </div>
        {fired && (
          <div style={{ marginTop: 12, fontSize: 12, color: "var(--green)" }}>
            ✓ Emitted "{fired}"
          </div>
        )}
      </div>
    </>
  );
}
