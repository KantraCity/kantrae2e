// Builds src/lib/emoji-data.json (emoji picker) from emojibase-data:
// node scripts/gen-emoji.mjs. The output is committed; the app loads it
// lazily the first time the emoji panel opens.
import { readFileSync, writeFileSync } from 'fs'
const ru = JSON.parse(readFileSync('node_modules/emojibase-data/ru/compact.json', 'utf8'))
const en = JSON.parse(readFileSync('node_modules/emojibase-data/en/compact.json', 'utf8'))
const enBy = new Map(en.map((e) => [e.hexcode, e]))
const out = ru
  .filter((e) => e.group !== undefined && e.group !== 2) // 2 = skin tone components
  .sort((a, b) => a.order - b.order)
  .map((e) => {
    const x = enBy.get(e.hexcode)
    // Search words: Russian label + tags, English label + tags.
    const words = [e.label, ...(e.tags ?? []), x?.label ?? '', ...(x?.tags ?? [])].join(' ').toLowerCase()
    const item = [e.unicode, e.group, words]
    if (e.skins?.length) item.push(e.skins.map((s) => s.unicode))
    return item
  })
writeFileSync('src/lib/emoji-data.json', JSON.stringify(out))
console.log(out.length, 'emoji')
