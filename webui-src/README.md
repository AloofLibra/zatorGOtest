# ZatorGO Web UI

Vue 3 + Vite. Сборка кладёт статику в `../assets/webui` (`app.js`, `styles.css`, `index.html`).

## Разработка

```sh
npm ci
npm run check
npm run dev      # proxy /api → :8099
npm run build
```

## Маршруты

| Path | View |
| --- | --- |
| `/` | OverviewView |
| `/strategies` | StrategiesView |
| `/sweep` | SupersweepView |
| `/custom-strats` | CustomStratsView |
| `/blobs-sni` (`alias /custom-blobs`) | BlobsSniView |
| `/domains/:list?` | DomainsView |
| `/settings/:panel?` | SettingsView |

API: только `/api/*` (см. `src/api/endpoints.ts`, `docs/API.md`).
