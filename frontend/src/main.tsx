// Must come first: in a browser tab it installs window.go / window.runtime
// before anything can call a generated binding. In the desktop app Wails has
// already injected both and the shim does nothing.
import './lib/webshim'
import React from 'react'
import {createRoot} from 'react-dom/client'
import './styles.css'
import App from './App'

const container = document.getElementById('root')

const root = createRoot(container!)

root.render(
    <React.StrictMode>
        <App/>
    </React.StrictMode>
)
