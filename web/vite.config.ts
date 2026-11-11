import { createHash } from 'node:crypto'
import fs from 'node:fs'
import path from 'node:path'
import { defineConfig } from 'vitest/config'
import type { Plugin } from 'vite'
import react from '@vitejs/plugin-react'
import tailwindcss from '@tailwindcss/vite'

const backend = 'http://localhost:8080'

/**
 * Writes dist/sw.js from sw/template.js. The version is a hash of what is in
 * dist, not the build time: a rebuild with no changes gives a byte-identical
 * sw.js, so the browser sees no update.
 */
function shellWorker(): Plugin {
  let outDir = ''
  return {
    name: 'wishbone-shell-worker',
    apply: 'build',
    configResolved(config) {
      outDir = path.resolve(config.root, config.build.outDir)
    },
    closeBundle() {
      const names = fs
        .readdirSync(outDir, { recursive: true, encoding: 'utf8' })
        .map((name) => name.split(path.sep).join('/'))
        .filter((name) => name !== 'sw.js' && fs.statSync(path.join(outDir, name)).isFile())
        .sort()

      const hash = createHash('sha256')
      for (const name of names) {
        hash.update(`${name}\0`)
        hash.update(fs.readFileSync(path.join(outDir, name)))
      }
      // The server redirects /index.html to /, and a redirect can't answer a
      // page load. So the cached shell has the key /.
      const files = names.map((name) => (name === 'index.html' ? '/' : `/${name}`))

      const template = fs.readFileSync(path.resolve(import.meta.dirname, 'sw/template.js'), 'utf8')
      const worker = template
        .replace('__VERSION__', hash.digest('hex').slice(0, 16))
        .replace(`'__FILES__'`, () => files.map((file) => `'${file}'`).join(', '))
      fs.writeFileSync(path.join(outDir, 'sw.js'), worker)
    },
  }
}

export default defineConfig({
  plugins: [react(), tailwindcss(), shellWorker()],
  build: {
    outDir: 'dist',
    sourcemap: false,
  },
  server: {
    port: 3000,
    proxy: {
      '/api': { target: backend, changeOrigin: true },
    },
  },
  test: {
    globals: true,
    environment: 'jsdom',
    setupFiles: './src/test/setup.ts',
    include: ['src/**/*.test.{ts,tsx}'],
    coverage: {
      provider: 'v8',
      reporter: ['text', 'html'],
    },
  },
})
