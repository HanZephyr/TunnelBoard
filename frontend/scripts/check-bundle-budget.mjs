import { gzipSync } from 'node:zlib'
import { readFileSync, readdirSync } from 'node:fs'
import { join } from 'node:path'
import { fileURLToPath } from 'node:url'

const KiB = 1024
const dist = new URL('../dist/', import.meta.url)
const manifest = JSON.parse(readFileSync(new URL('manifest.json', dist), 'utf8'))
const entry = Object.values(manifest).find((item) => item.isEntry)
if (!entry?.file) throw new Error('bundle budget: Vite manifest has no entry')

const files = readdirSync(new URL('assets/', dist)).filter((name) => /\.(js|css|woff2?|ttf)$/.test(name))
const rows = files.map((name) => {
  const data = readFileSync(join(fileURLToPath(new URL('assets/', dist)), name))
  return { name, bytes: data.length, gzip: /\.(js|css)$/.test(name) ? gzipSync(data).length : 0 }
})
const errors = []
for (const row of rows) {
  if (row.name === entry.file.replace('assets/', '')) {
    if (row.bytes > 250 * KiB) errors.push(`entry JS ${row.name} is ${(row.bytes / KiB).toFixed(2)} KiB > 250 KiB`)
    if (row.gzip > 90 * KiB) errors.push(`entry JS gzip is ${(row.gzip / KiB).toFixed(2)} KiB > 90 KiB`)
  } else if (row.name.endsWith('.js') && row.bytes > 200 * KiB) {
    errors.push(`async/shared JS ${row.name} is ${(row.bytes / KiB).toFixed(2)} KiB > 200 KiB`)
  }
  if (row.name.endsWith('.js') && row.bytes >= 500 * KiB) errors.push(`${row.name} reached 500 KiB`)
  if (row.name.startsWith('index.') && row.name.endsWith('.css')) {
    if (row.bytes > 360 * KiB) errors.push(`entry CSS is ${(row.bytes / KiB).toFixed(2)} KiB > 360 KiB`)
    if (row.gzip > 55 * KiB) errors.push(`entry CSS gzip is ${(row.gzip / KiB).toFixed(2)} KiB > 55 KiB`)
  }
}

const total = (suffix) => rows.filter((row) => row.name.endsWith(suffix)).reduce((sum, row) => sum + row.bytes, 0)
console.log(`bundle budget: JS ${(total('.js') / KiB).toFixed(2)} KiB, CSS ${(total('.css') / KiB).toFixed(2)} KiB, fonts ${((total('.woff') + total('.woff2')) / KiB).toFixed(2)} KiB`)
if (errors.length) throw new Error(`bundle budget failed:\n${errors.join('\n')}`)
