import { StrictMode } from 'react'
import { createRoot } from 'react-dom/client'
import './index.css'
import { App } from './App'
import { UpdateNotifier } from './UpdateNotifier'
import { watchForUpdates } from './updates'

// Without a worker, as over plain HTTP or in `just dev-frontend`, there is no
// cached shell, so there is nothing to update.
if (import.meta.env.PROD && 'serviceWorker' in navigator) watchForUpdates()

createRoot(document.getElementById('root')!).render(
  <StrictMode>
    <App />
    <UpdateNotifier />
  </StrictMode>,
)
