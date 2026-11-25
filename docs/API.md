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
| POST | `/api/check` | сетевая проверка; профиль 2 — Innertube/media-range, профиль 5 — HTTP/3 (QUIC) через `h3-curl` (`video_id`/`media_url`, `verdict=unknown` при отсутствии потока) |
| GET/POST | `/api/custom` | пользовательские стратегии (`tpl/p4/p5/p6/p7`): список по блоку (`?block=`), `add`, `edit`, `enable`, `delete`, `import` (blockcheck2/nfqws2), `apply` (инжект + ремап локов) |
| GET/POST | `/api/blobs` | пользовательские блобы (`custom_blobs.tsv` + `custom_blob/`), SNI-пул и наборы (`sni_zator.txt`/`sni_user.txt`): `upload` (binary `application/octet-stream`, 1 Б..256 КиБ), `add`, `edit`, `delete`, `apply`, `sni_pin`, `sni_pool_assign`, `sni_pool_custom`, `sni_pool_clear`, `sni_pool_policy`, `sni_set_write`, `sni_set_delete` |
| GET/POST | `/api/domains` | списки доменов (`action=refresh_ips` — googlevideo-ipv4 для WSSIZE) |
| GET/POST | `/api/backups` | бэкапы (вкл. `custom_blobs`, `custom_strategies`, `blob_files`, `sni_pool*`, `sni_user.txt`, `WSSIZE_RAMP`) |
| GET | `/api/scopes` | strategy lock scopes and client scope settings |
| GET/POST | `/api/client-scopes` | list IP→mark mappings; POST `action=add|remove`, `ip`, and for add `scope=mark:N`; mapping changes restart zapret2 unless `restart=0` |
| GET/POST | `/api/settings` | настройки (`setting=…`: в т.ч. `tls_blob` с `quic_blobs` и `tls_blob_profile` для П5 `quic_*`/`discord_udp_*`, `h3_curl` `install\|update\|remove`, `nfqws2_errors` с `suppressed_conntrack`) |
| POST | `/api/settings` (`setting=supersweep`) | full run/status/cancel; `quick_profile=1..5` (`address` для 1–4; 5 — live HTTP/3 QUIC), `include_quic=0\|1`, `quic_rounds=1..5`, `tls_pref=any\|12\|13\|both` |
| POST | `/api/blob-upload` | загрузка TLS/WG blob (legacy/compat для настроек TLS/WG) |
| POST | `/api/login` | вход по паролю |
| POST | `/api/logout` | выход (ротация session) |

Типы ответов UI: `webui-src/src/api/types.ts`. Клиент: `webui-src/src/api/endpoints.ts`.
