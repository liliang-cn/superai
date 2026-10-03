// Must come first: in a browser tab it installs window.go / window.runtime
// before anything can call a generated binding. In the desktop app Wails has
// already injected both and the shim does nothing.
import './lib/webshim'
import React, {useEffect, useState} from 'react'
import {createRoot} from 'react-dom/client'
import {HashRouter} from 'react-router-dom'
import '@fontsource-variable/geist'
import '@fontsource-variable/geist-mono'
import './styles.css'
import App from './App'
import Gate from './components/Gate'
import LinkHive from './components/LinkHive'
import {HiveLinkStatus, HiveLinks} from '../wailsjs/go/app/App'
import {installHiveBridge, setAlone, wantsAlone} from './lib/hivelink'

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
    // The desktop window draws its own title bar (main.go asks for
    // TitleBarHiddenInset), which means the sidebar has to leave a lane clear
    // for the traffic lights and mark it draggable. A browser tab has neither,
    // so the flag is set once here rather than guessed at in CSS.
    useEffect(() => {
        document.body.classList.toggle('desktop-shell', !served)
    }, [served])
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

    // The desktop window: onto the hive it is linked to, or asking which one.
    // null = still finding out.
    const [link, setLink] = useState<'linked' | 'alone' | 'ask' | null>(served ? 'alone' : null)
    useEffect(() => {
        if (served) return
        // Linked: onto that hive. Not linked but with hives saved, or told
        // once to stay on its own: this Mac. Otherwise ask.
        Promise.all([HiveLinkStatus(), HiveLinks().catch(() => [])])
            .then(([s, saved]) => {
                if (s?.linked) {
                    installHiveBridge()
                    setLink('linked')
                } else setLink(wantsAlone() || (saved ?? []).length > 0 ? 'alone' : 'ask')
            })
            .catch(() => setLink('alone'))
    }, [served])

    if (authed === null || link === null) return null
    if (!authed) return <Gate onEnter={() => setAuthed(true)}/>
    if (link === 'ask') {
        return <LinkHive
            onLinked={() => { installHiveBridge(); setLink('linked') }}
            onAlone={() => { setAlone(true); setLink('alone') }}/>
    }
    return (
        <HashRouter>
            <App/>
        </HashRouter>
    )
}

const container = document.getElementById('root')

const root = createRoot(container!)

root.render(
    <React.StrictMode>
        <Root/>
    </React.StrictMode>
)
