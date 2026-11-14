import { createHash } from 'node:crypto'
import { readFileSync, writeFileSync } from 'node:fs'
import path from 'node:path'
import { fileURLToPath } from 'node:url'

const webuiDir = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '../../assets/webui')

const digest = (name) =>
  createHash('sha256').update(readFileSync(path.join(webuiDir, name))).digest('hex').slice(0, 8)

let html = readFileSync(path.join(webuiDir, 'index.html'), 'utf8')

// classic-скрипт обязан идти в конец body: app.js монтируется синхронно в #app
html = html.replace(/<script[^>]*src="[^"]*app\.js[^"]*"[^>]*><\/script>\s*/g, '')
// только локальный styles.css (не трогаем Google Fonts и прочие внешние CSS)
html = html.replace(/<link[^>]*rel="stylesheet"[^>]*href="\.?\/?styles\.css[^"]*"[^>]*>\s*/gi, '')
html = html.replace(/<link[^>]*href="\.?\/?styles\.css[^"]*"[^>]*rel="stylesheet"[^>]*>\s*/gi, '')
html = html.replace('</head>', `  <link rel="stylesheet" href="styles.css?v=${digest('styles.css')}">\n</head>`)
html = html.replace('</body>', `  <script src="app.js?v=${digest('app.js')}"></script>\n</body>`)
html = html.replace(/(<link[^>]*href=")favicon\.png([^"]*")/,
  `$1favicon.png?v=${digest('favicon.png')}$2`)
html = html.replace(/(<link[^>]*href=")icon\.png([^"]*")/,
  `$1icon.png?v=${digest('icon.png')}$2`)

writeFileSync(path.join(webuiDir, 'index.html'), html)
console.log(`app.js?v=${digest('app.js')}`)
console.log(`styles.css?v=${digest('styles.css')}`)
console.log(`favicon.png?v=${digest('favicon.png')}`)
console.log(`icon.png?v=${digest('icon.png')}`)
