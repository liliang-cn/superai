// See app.ts: the embed's stand-in for the Wails runtime.

export const ClipboardSetText = (_: string) => Promise.resolve(false);
export const EventsOn = (_name: string, _cb: (...data: unknown[]) => void) => () => {};
