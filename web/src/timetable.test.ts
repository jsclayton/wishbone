/// <reference types="node" />
import { readFileSync } from 'node:fs'
import { describe, expect, it } from 'vitest'

// Vitest runs from web/. The CSS is read as text because Vite would process it.
const read = (file: string) => readFileSync(file, 'utf8')
const indexHtml = read('index.html')
const manifestJson = read('public/manifest.webmanifest')
const indexCss = read('src/index.css')

const TOKENS = ['bg', 'ink', 'mute', 'hair', 'field', 'tile', 'band', 'on-band', 'band-mute', 'btn', 'on-btn', 'alert', 'on-alert']

describe('index.css', () => {
  const css = indexCss

  it('defines every token for light and again for dark', () => {
    const dark = css.slice(css.indexOf('prefers-color-scheme: dark'))
    expect(dark).toContain('prefers-color-scheme: dark')
    for (const token of TOKENS) {
      expect(css, `light --color-${token}`).toMatch(new RegExp(`--color-${token}:\\s*#`))
      expect(dark, `dark --color-${token}`).toMatch(new RegExp(`--color-${token}:\\s*#`))
    }
  })

  it('names the two Atkinson families', () => {
    expect(css).toContain('Atkinson Hyperlegible Next')
    expect(css).toContain('Atkinson Hyperlegible Mono')
  })

  it('loads fonts from the package, never from another host', () => {
    expect(css).toContain('@fontsource/atkinson-hyperlegible-next')
    expect(css).toContain('@fontsource/atkinson-hyperlegible-mono')
    expect(css).not.toMatch(/https?:\/\//)
    expect(indexHtml).not.toMatch(/fonts\.(googleapis|gstatic)/)
  })
})

describe('font licenses', () => {
  it.each(['next', 'mono'])('ships the license of %s', (name) => {
    expect(read(`public/licenses/atkinson-hyperlegible-${name}.txt`)).toContain('SIL Open Font License')
  })
})

describe('band colors', () => {
  const band = '#2b3a47'
  it('match in the manifest and the theme-color meta tags', () => {
    const manifest = JSON.parse(manifestJson)
    expect(manifest.theme_color.toLowerCase()).toBe(band)
    expect(manifest.background_color.toLowerCase()).toBe(band)
    const metas = [...indexHtml.matchAll(/<meta name="theme-color"[^>]*content="([^"]+)"/g)]
    expect(metas.length).toBeGreaterThan(0)
    for (const m of metas) expect(m[1]!.toLowerCase()).toBe(band)
  })
})
