import { StrictMode } from 'react'
import { createRoot } from 'react-dom/client'
// The font is served by the factory itself: the host is reached over the tailnet, the browser that
// opens it has no JetBrains Mono of its own, and a screenshot has to look the same everywhere.
import '@fontsource/jetbrains-mono/latin-400.css'
import '@fontsource/jetbrains-mono/latin-700.css'
import App from './App.jsx'
import './styles.css'

createRoot(document.getElementById('root')).render(
  <StrictMode>
    <App />
  </StrictMode>,
)
