package app

// A second ask while the queen is still answering the first.
//
// It starts at once, on its own goroutine, like any other turn: nobody waits
// behind an answer still being written. The agent takes several runs in one
// conversation at the same time (backend/concurrent_test.go).
//
// It used to wait in line for the running turn, and before that it went to an
// idle worker. A worker has no agent link and none of the queen's machine
// tools: asked to update something on the Mac while the queen was doing just
// that, one answered that the Mac was not connected. So it is still the
// queen's turn, never a worker's. With a queen engine set, the engine's session
// takes one turn at a time, and this app's model stands in for it while it is
// busy (startTurn in app.go).
