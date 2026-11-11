import '@testing-library/jest-dom/vitest'
import { browserZone } from './browserZone'

// jsdom has no matchMedia, and the top row asks it whether this is the installed app.
window.matchMedia = ((query: string) => ({ matches: false, media: query }) as MediaQueryList)

// The app reports the browser's timezone when it differs from the user's (see App).
// Here the browser can't tell its zone, so a test does not depend on the machine's
// and sees no report. A test of the report sets the zone itself.
beforeEach(() => browserZone(undefined))
