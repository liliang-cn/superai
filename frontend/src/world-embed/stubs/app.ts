// The world embed has no core to call: the host hands it everything it
// shows. The few bindings the scene's helpers reach for answer "not here",
// and they fall back to what the host sent.

const none = () => Promise.reject(new Error("world embed: no core"));

export const QueenIdentity = none;
