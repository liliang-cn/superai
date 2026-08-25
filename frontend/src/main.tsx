// Must come first: in a browser tab it installs window.go / window.runtime
// before anything can call a generated binding. In the desktop app Wails has
// already injected both and the shim does nothing.
import './lib/webshim'
import React, {useEffect, useState} from 'react'
import {createRoot} from 'react-dom/client'
import './styles.css'
import App from './App'
import Gate from './components/Gate'

/**
 * The password box, and only then the app.
 *
 * The gate sits above App rather than inside it on purpose: App opens the SSE
 * stream and starts calling bound methods the moment it mounts, and every one
 * of those is refused until there is a session. Mounting it behind the gate
 * would mean a screenful of failed calls and a stream retrying in a loop
 * behind the password box.
 *
 * Only the served build has a door. In the desktop window `superaiServed` is
 * never set and this resolves to the app immediately — the machine is already
 * yours.
 */
function Root() {
    const served = Boolean((window as unknown as Record<string, unknown>).superaiServed)
    // null = still asking. Rendering the gate first and taking it away would
    // flash a password box at someone who is already signed in.
    const [authed, setAuthed] = useState<boolean | null>(served ? null : true)

    useEffect(() => {
        if (!served) return
        fetch('/api/session')
            .then((r) => r.json())
            .then((d: { authed?: boolean }) => setAuthed(Boolean(d.authed)))
            .catch(() => setAuthed(false))
    }, [served])

    if (authed === null) return null
    if (!authed) return <Gate onEnter={() => setAuthed(true)}/>
    return <App/>
}

const container = document.getElementById('root')

const root = createRoot(container!)

root.render(
    <React.StrictMode>
        <Root/>
    </React.StrictMode>
)
