// Renders the PNG icons in web/public from web/public/favicon.svg.
// Run with `just icons`. It uses Playwright's Chromium, so it needs no
// install scripts beyond `just install-playwright`.
import { chromium } from '@playwright/test'
import fs from 'node:fs'
import path from 'node:path'

const publicDir = path.resolve(import.meta.dirname, '../public')
const svg = fs.readFileSync(path.join(publicDir, 'favicon.svg'), 'utf8')
// Strip the dark-mode style: icons always use the light scheme.
const mark = svg.replace(/<style>.*<\/style>/s, '').replace('currentColor', '#222222')
const inner = mark.replace(/^<svg[^>]*>/, '').replace(/<\/svg>$/, '').trim()

const background = '#ffffff'
// scale: share of the canvas the 64-unit mark box fills. The maskable icon
// keeps the mark inside the central 80% circle that masks may crop to.
const icons = [
  { file: 'apple-touch-icon.png', size: 180, scale: 0.8 },
  { file: 'icon-192.png', size: 192, scale: 0.8 },
  { file: 'icon-512.png', size: 512, scale: 0.8 },
  { file: 'icon-maskable-512.png', size: 512, scale: 0.55 },
]

const browser = await chromium.launch()
const page = await browser.newPage()
for (const { file, size, scale } of icons) {
  const box = 64 / scale
  const offset = (box - 64) / 2
  const html = `<body style="margin:0"><svg xmlns="http://www.w3.org/2000/svg" width="${size}" height="${size}" viewBox="${-offset} ${-offset} ${box} ${box}"><rect x="${-offset}" y="${-offset}" width="${box}" height="${box}" fill="${background}"/>${inner}</svg></body>`
  await page.setViewportSize({ width: size, height: size })
  await page.setContent(html)
  fs.writeFileSync(path.join(publicDir, file), await page.screenshot({ omitBackground: false }))
  console.log(`wrote ${file}`)
}
await browser.close()
