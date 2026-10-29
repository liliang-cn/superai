// Must come first: in a browser tab it installs window.go / window.runtime
// before anything can call a generated binding. In the desktop app Wails has
// already injected both and the shim does nothing.
import './lib/webshim'
import React, {useEffect, useState} from 'react'
import {createRoot} from 'react-dom/client'
import {bootAfterMount} from './lib/boot'
import {HashRouter} from 'react-router-dom'
import '@fontsource-variable/geist'
import '@fontsource-variable/geist-mono'
import './styles.css'
import App from './App'
import Gate from './components/Gate'
import {HiveMembership, OnboardingNeeded, TitleBarDoubleClick} from '../wailsjs/go/app/App'
import {installHiveBridge, installTitleBarDoubleClick} from './lib/hivelink'
import Onboarding from './components/Onboarding'
import {SETUP_EVENT} from './lib/ready'
import {installImeEnterGuard} from './lib/ime'

installImeEnterGuard()

/**
 * The password box, and only then the app.
 *
 * The gate sits above App rather than inside it on purpose: App opens the SSE
 * stream and starts calling bound methods the moment it mounts, and every one
 * of those is refused until there is a session. Mounting it behind the gate
 * would mean a screenful of failed calls and a stream retrying in a loop
 * behind the password box.
 *
 * Only the served build has a door. In the desktop window `superleoServed` is
 * never set and this resolves to the app immediately — the machine is already
 * yours.
 */
function Root() {
    const served = Boolean((window as unknown as Record<string, unknown>).superleoServed)
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

    // The desktop window: onto the hive this Mac has joined, else onto this
    // Mac's own queen. false = still finding out.
    const [placed, setPlaced] = useState<boolean>(served)
    // The first start's questions not asked yet: those first, the same in a
    // browser (the agents are those on the machine SuperLeo runs on).
    const [onboard, setOnboard] = useState(false)
    const [onboardAt, setOnboardAt] = useState<'ask' | 'model'>('ask')
    useEffect(() => {
        if (!served || !authed) return
        OnboardingNeeded().then(setOnboard).catch(() => setOnboard(false))
    }, [served, authed])
    // The setup banner, or a message sent with nothing to think with.
    useEffect(() => {
        const open = () => { setOnboardAt('ask'); setOnboard(true) }
        window.addEventListener(SETUP_EVENT, open)
        return () => window.removeEventListener(SETUP_EVENT, open)
    }, [])
    useEffect(() => {
        if (served) return
        HiveMembership()
            .then(async (m) => {
                if (m?.joined) installHiveBridge()
                else setOnboard(await OnboardingNeeded().catch(() => false))
            })
            .catch(() => {})
            .finally(() => setPlaced(true))
    }, [served])

    if (authed === null || !placed) return null
    if (!authed) return <Gate onEnter={() => setAuthed(true)}/>
    if (onboard) return <Onboarding web={served} start={onboardAt} onDone={() => setOnboard(false)}/>
    return (
        <HashRouter>
            <App/>
        </HashRouter>
    )
}

// The desktop window's drag strips behave like a Mac title bar.
if (!(window as unknown as Record<string, unknown>).superleoServed) {
    installTitleBarDoubleClick(() => { void TitleBarDoubleClick().catch(() => {}) })
}

const container = document.getElementById('root')

const root = createRoot(container!)

root.render(
    <React.StrictMode>
        <Root/>
    </React.StrictMode>
)
bootAfterMount()
