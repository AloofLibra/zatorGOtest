# ZatorGO HTTP API

Канонические пути: `/api/<name>`. Алиасы `/cgi-bin/<name>.cgi` сохранены для совместимости.

Авторизация при `auth_enabled=true`: cookie `zator_session`, `Authorization: Bearer <session_token>`, или HTTP Basic с паролем. Статика SPA (index/js/css) доступна без cookie.

| Метод | Путь | Назначение |
| --- | --- | --- |
| GET | `/api/status` | статус / профили / локи |
| GET | `/api/state` | агрегат для UI |
| POST | `/api/set-lock` | фиксация стратегии |
| POST | `/api/clear-lock` | сброс лока |
| POST | `/api/service` | start/stop/restart zapret2 |
| POST | `/api/check` | сетевая проверка |
| GET/POST | `/api/domains` | списки доменов |
| GET/POST | `/api/backups` | бэкапы |
| GET | `/api/scopes` | client scopes |
| GET/POST | `/api/settings` | настройки (`setting=…`) |
| POST | `/api/blob-upload` | загрузка TLS/WG blob |
| POST | `/api/login` | вход по паролю |
| POST | `/api/logout` | выход (ротация session) |

Типы ответов UI: `webui-src/src/api/types.ts`. Клиент: `webui-src/src/api/endpoints.ts`.
