// Browser-mode implementations of the two globals the generated wailsjs
// bindings resolve at call time: window.go (method bindings) and
// window.runtime (events & helpers).
//
// Inside the desktop app Wails injects both before any module script runs, so
// the `!window.go` gate below never fires there. In a plain browser tab —
// served by `superai-desktop serve` — this shim takes their place:
//
//   window.go.main.App.<Method>(...)  ->  POST /api/rpc/<Method>  (JSON array in,
//                                         JSON out, non-2xx rejects the promise
//                                         with the response text — the same
//                                         resolve/reject shape Wails produces)
//   EventsOn(name, cb)                ->  one shared SSE stream on /api/events
//                                         carrying {name, payload} envelopes
//
// Nothing else in the frontend knows which transport it is on. Keep it that
// way: new backend calls go through the generated bindings, never fetch().

type EventCallback = (...data: unknown[]) => void;

function installWebShim() {
  const rpc = async (name: string, args: unknown[]): Promise<unknown> => {
    const resp = await fetch(`/api/rpc/${encodeURIComponent(name)}`, {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(args),
    });
    // The session expired (or was signed out in another tab). Every call from
    // here on would fail the same way, so reload: the shell asks /api/session,
    // gets a no, and draws the password box instead of a screen of errors.
    if (resp.status === 401) {
      window.location.reload();
      throw new Error("signed out");
    }
    const text = await resp.text();
    if (!resp.ok) {
      throw new Error(text.trim() || `${name}: HTTP ${resp.status}`);
    }
    return text === "" ? undefined : JSON.parse(text);
  };

  const appProxy = new Proxy(
    {},
    {
      get:
        (_target, prop) =>
        (...args: unknown[]) =>
          rpc(String(prop), args),
    },
  );

  const listeners = new Map<string, Set<EventCallback>>();
  let source: EventSource | null = null;
  const ensureStream = () => {
    if (source) return;
    source = new EventSource("/api/events");
    source.onmessage = (e: MessageEvent) => {
      let envelope: { name?: string; payload?: unknown };
      try {
        envelope = JSON.parse(e.data);
      } catch {
        return;
      }
      if (!envelope.name) return;
      listeners.get(envelope.name)?.forEach((cb) => {
        try {
          cb(envelope.payload);
        } catch (err) {
          console.error(`event handler for ${envelope.name}:`, err);
        }
      });
    };
    // EventSource reconnects on its own; nothing to do on error beyond not
    // tearing the listener map down.
  };

  const eventsOn = (name: string, cb: EventCallback): (() => void) => {
    ensureStream();
    let set = listeners.get(name);
    if (!set) {
      set = new Set();
      listeners.set(name, set);
    }
    set.add(cb);
    return () => {
      set!.delete(cb);
    };
  };

  const runtimeShim = {
    EventsOn: eventsOn,
    EventsOnMultiple: (name: string, cb: EventCallback, _max: number) => eventsOn(name, cb),
    EventsOnce: (name: string, cb: EventCallback) => {
      const off = eventsOn(name, (...data) => {
        off();
        cb(...data);
      });
      return off;
    },
    EventsOff: (name: string, ...more: string[]) => {
      [name, ...more].forEach((n) => listeners.delete(n));
    },
    EventsOffAll: () => listeners.clear(),
    EventsEmit: (_name: string, ..._data: unknown[]) => {
      // Frontend-to-backend events are not part of this app's protocol; every
      // call goes through a bound method instead.
    },
    BrowserOpenURL: (url: string) => {
      window.open(url, "_blank", "noopener,noreferrer");
    },
    ClipboardSetText: (text: string) => navigator.clipboard.writeText(text).then(() => true),
    ClipboardGetText: () => navigator.clipboard.readText(),
    // Native OS file drop does not exist in a tab; the drop targets simply
    // never receive host paths, matching an app run without drag-and-drop.
    OnFileDrop: (_cb: unknown, _useDropTarget?: boolean) => {},
    OnFileDropOff: () => {},
    WindowSetTitle: (title: string) => {
      document.title = title;
    },
    Environment: () =>
      Promise.resolve({ buildType: "production", platform: "web", arch: "" }),
    Quit: () => {},
    LogPrint: console.log,
    LogTrace: console.debug,
    LogDebug: console.debug,
    LogInfo: console.info,
    LogWarning: console.warn,
    LogError: console.error,
    LogFatal: console.error,
  };

  const w = window as unknown as Record<string, unknown>;
  w.go = { main: { App: appProxy } };
  w.runtime = runtimeShim;
  // Served over HTTP, which is the only mode with a door on it. The desktop
  // window never sets this, and so never asks for a password.
  w.superaiServed = true;
}

if (!(window as unknown as Record<string, unknown>).go) {
  installWebShim();
}

export {};
