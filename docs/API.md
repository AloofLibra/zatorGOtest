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
| POST | `/api/check` | сетевая проверка; профиль 2 автоматически получает подписанные YouTube audio/video URL через Innertube. Опционально принимает `video_id` или подписанный `media_url`; `verdict=unknown` означает, что медиа получить/подтвердить не удалось. |
| GET/POST | `/api/domains` | списки доменов (`action=refresh_ips` — googlevideo-ipv4 для WSSIZE) |
| GET/POST | `/api/backups` | бэкапы |
| GET | `/api/scopes` | client scopes |
| GET/POST | `/api/settings` | настройки (`setting=…`) |
| POST | `/api/settings` (`setting=supersweep`) | full run/status/cancel; `quick_profile=1|2|3|4` с `address` запускает короткий перебор одной цели через тот же coordinator, `tls_pref=any|12|13|both` задаёт TLS preference, зелёный победитель применяется автоматически |
| POST | `/api/blob-upload` | загрузка TLS/WG blob |
| POST | `/api/login` | вход по паролю |
| POST | `/api/logout` | выход (ротация session) |

Типы ответов UI: `webui-src/src/api/types.ts`. Клиент: `webui-src/src/api/endpoints.ts`.
