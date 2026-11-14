# ZatorGO HTTP API

Канонические пути: `/api/<name>`. Алиасы `/cgi-bin/<name>.cgi` сохранены для совместимости.

Авторизация при `auth_enabled=true`: cookie `zator_session`, `Authorization: Bearer <session_token>`, или HTTP Basic с паролем. Статика SPA (index/js/css) доступна без cookie.

| Метод | Путь | Назначение |
| --- | --- | --- |
| GET | `/api/status` | статус / профили / локи (`service_warnings`, `nfqws2_errors`, `post_update_verify`) |
| GET | `/api/state` | агрегат для UI |
| POST | `/api/set-lock` | фиксация стратегии |
| POST | `/api/clear-lock` | сброс лока |
| POST | `/api/service` | start/stop/restart zapret2 |
| POST | `/api/check` | сетевая проверка (`profile=2&media_url=` — Range 16K/64K googlevideo) |
| GET/POST | `/api/domains` | списки доменов (`action=refresh_ips` — googlevideo-ipv4 для WSSIZE) |
| GET/POST | `/api/backups` | бэкапы |
| GET | `/api/scopes` | client scopes |
| GET/POST | `/api/settings` | настройки (`setting=…`) |
| POST | `/api/blob-upload` | загрузка TLS/WG blob |
| POST | `/api/login` | вход по паролю |
| POST | `/api/logout` | выход (ротация session) |

Типы ответов UI: `webui-src/src/api/types.ts`. Клиент: `webui-src/src/api/endpoints.ts`.
