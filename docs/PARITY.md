# Чеклист паритета: bash zator → Go zator

Референс: ветка `feat/googlevideo-fix` репозитория AloofLibra/zator.
Цель: весь управляющий функционал доступен через bootstrap CLI + Web UI.

## Аудит перехода на `zatorGO` (2026-11-16)

| Функция | Bash `feat/googlevideo-fix` | Go `zatorGO` | Пробел / действие |
| --- | --- | --- | --- |
| Vue 3 WebUI и API | Полный интерфейс Zator | Основные экраны/API перенесены; Vite build, vue-tsc и npm audit проходят; роутер отдаёт assets с совпавшими SHA-256 | Продолжить контрактную проверку сценариев |
| YouTube media | Innertube, подписанные audio/video URL, ограниченная загрузка | Profile 2 и SuperSweep получают signed URL, проверяют audio 256 KiB, video 1 MiB и video middle 2 MiB; валидируют 206, точный Content-Range, MIME и объём. SuperSweep повторяет всю тройку отдельно по TLS 1.2 и 1.3; CDN redirect ограничен двумя подписанными Googlevideo URL. Resolver сохраняет длины и MIME форматов; DNS/API/подпись/HTTP-валидность дают unknown, обрыв тела и сетевой сбой классифицируются отдельно | На тестовом роутере новая ARM64-медиапроба через `AWG_pers` прошла все 6 точных Range-запросов; воспроизведение в клиенте и профиль через WAN ещё не подтверждены |
| Blockcheck2 HTTPS lists | Установка `10-list.sh` и списков TLS 1.2/1.3 в zapret2 перед запуском `blockcheck2.sh` | Go CLI передаёт upstream имя хоста; на OpenWrt ARM64 `example.com` прошёл TLS 1.2 и TLS 1.3 (`working without bypass`), SUMMARY сохранён, procd-служба восстановлена | Отдельный port-check недоступен без ncat; DPI-blocked hostname требует отдельного тестового окна/цели |
| Быстрый подбор | Однопрофильный/доменный подбор, отмена, применение результата | WebUI запускает один профиль и адрес через существующий coordinator; на OpenWrt ARM64 проверены API start/cancel, отдельный probe и cleanup; профиль 2 закончил все 44 стратегии, прогресс был доступен, зелёных кандидатов не нашлось | Применение зелёного победителя не проверено, так как реальная цель Googlevideo недоступна из роутера |
| WSSIZE Full/Lite | Full/Lite, SYN/ACK, window scale, nftables/iptables, Googlevideo set, toggles | После reboot тестового OpenWrt qnum 300 работает с `wssize_ramp:mode=full:wsize=1:scale=2:open=1`; API/config согласованы, 924 свежих IP, nft mark/queue rules присутствуют | Реальная потоковая передача и нагрузка с Full ещё не измерены |
| Авторотация/детекторы | Lua `circular_quality`, success/failure detectors, SLM | Текущие Lua assets сохранены; Go seed пишет provenance, learned-only transfer; validator service доступен | Проверить ложноположительные срабатывания и client-scope на реальном трафике |
| Пользовательские настройки | Профили, blob/SNI/clone, DNS/RST/reassembly/fallback/WG/QUIC/UDP, scopes, provider, telemetry, backup | Существующие WebUI/API/config backend и contract tests сохранены; требуется сквозное применение на платформах | Полный settings round-trip на роутере ещё не проверен |
| Аварийный CLI | Статус, ошибки, службы, сброс локов, restore/rollback | `status`, `service`, `locks clear -y` (создаёт backup), `restore` (smart), `rollback` доступны без браузера | Лог службы зависит от platform backend; CLI пока показывает сохранённые ошибки Zator, не системный journal |
| SuperSweep | Фазы, media probe, локи, прогресс и изолированный transport | На OpenWrt ARM64 быстрый профиль 2 завершил 44/44 стратегии без ошибок и применил стратегию 18 только в изолированный `mark:200`; затем полный прогон выбрал стратегию 36 по 44/44 успешным YouTube-проверкам. Googlevideo-фаза через WAN дала 6/6 timeout и была отменена; API очистил probe/qnum 65535, lock `mark:200` снят, рабочий qnum 300 и `client_scope=off` сохранены | Успешный winner/apply подтверждён для YouTube-фазы; полный многопрофильный прогон не завершён из-за недоступности Googlevideo через основной WAN |
| Googlevideo collector/seed | UBus/AF_PACKET, queue/upload, центральный IP-set и проверка/apply стратегий | A/CNAME parser, TTL/public-IP checks, bounded RAM dedupe, tmpfs queue, central feed, UBus/AF_PACKET reconnect; consent/token-gated 3-hour upload; new IPs merge incrementally into nft set/ipset. Background seed transfers only a learned/legacy majority from askey 2/default scope; rows stay marked `seed`, state is persisted before live TSV, and missing majority with no probe remains `unknown` | Поведение на Keenetic и доставка наблюдений серверу требуют platform E2E |
| Установка, обновление, откат | Bash installer и rollback | CLI/offline/update; update требует product backup + бинарный snapshot; полный rollback config/lists/bin покрыт тестом; uninstall прекращается при ошибке stop/disable | Полный release update/rollback на роутере ещё не выполнен |
| OpenWrt/Keenetic | Проверены отдельные платформенные сценарии | На тестовом OpenWrt ARM64 WebUI и zapret2 автоматически поднялись после reboot; WebUI отвечает, WSSIZE Full и nft queue rules применены, assets совпали по SHA-256 | Release update/rollback на роутере и Keenetic E2E не выполнены |
| Нагрузка слабого роутера | Измерения зависят от устройства | На тестовом OpenWrt ARM64 после обновления WebUI: RSS zator 15.2 MiB, 7 threads; system idle 90% в двух снимках с интервалом 5 секунд | Устройство имеет 496716 KiB RAM; это не целевой 128-MiB роутер. Collector и watchdog в idle-замере отдельно не измерялись |

На тестовом OpenWrt 25.12.4/ARM64 profile-2 API check через основной WAN после reboot вернул `unknown`: TLS к Googlevideo не подтверждён, медиаданные через этот WAN не получены. После reboot и с активным `WSSIZE enabled=true, mode=full, scale=2` медиапроба через `AWG_pers` (`10.8.1.14`) успешно получила TLS 1.2 и 1.3 и точные HTTP 206 для audio 256 КиБ, video 1 МиБ и middle video 2 МиБ (`play/ok`); клиентское воспроизведение не проверено. Default TLS/download timeout — 10 с, TCP connect timeout — 3 с. Go install сохраняет upstream `/etc/init.d/zapret2` для nftables и custom.d. После reboot API/config показывают WSSIZE Full, 924 свежих Googlevideo IPv4; live qnum 300 использует `wssize_ramp:mode=full:wsize=1:scale=2:open=1`, nft mark/queue rules активны. WebUI и zapret2 автоматически поднялись; статика пережила reboot и совпала по SHA-256. Изолированный быстрый профиль прошёл 44/44; полный SuperSweep остановлен после 6/44 Googlevideo timeout через основной WAN, остальные фазы не запускались. Probe qnum 65535 и временный `mark:200` lock очищены; рабочий qnum 300 сохранён. Сохранён rollback backup `/opt/zator_backup/zatorGO-runtime-pre-20261115`; старый сервис `zator-go` отключён. Полное клиентское воспроизведение, release update/rollback и Keenetic остаются незавершёнными.

Для сборки проверены все 10 целей `scripts/crossbuild.sh` (arm, arm64, mips, mips64, mipsel, mipsel64, ppc, 386, amd64, riscv64); бинарники создавались во временном каталоге Windows-хоста. Это подтверждает компиляцию, но запуск на MIPS/ARM и размер runtime-памяти там не проверены.

## Bootstrap / лаунчер

| Функция | Статус | Где |
| --- | --- | --- |
| Определение arch + скачивание бинарника | done | `scripts/zator-launcher.sh` |
| `zator install` (каталоги, assets, password auth) | done | `cmd/zator`, `internal/app` |
| Установка zapret2 без `install_easy.sh` | done | `internal/zapret`, `-zapret` |
| Service hooks systemd/procd/Entware/Merlin/Keenetic | done | `internal/service` |
| `zator webui` + URL/token | done | `cmd/zator`, `internal/webapi` |
| `zator update` / self-update tar | done | `internal/deploy` |
| Offline bundle (`Z2R_OFFLINE`, vendor/) | done | `internal/deploy` |
| Cross-build arm/arm64/mips*/ppc/x86/x86_64 | done | `scripts/crossbuild.sh`, CI |

## Web UI / API (контракт CGI)

| Эндпоинт | Статус |
| --- | --- |
| status / state | done |
| set-lock / clear-lock | done |
| service start/stop/restart | done |
| check (TLS 1.2/1.3) | done |
| Profile 2 YouTube media check | verified after reboot with Full WSSIZE through `AWG_pers`: TLS 1.2/1.3 and signed media ranges succeeded; primary WAN times out and client playback is unverified |
| Blockcheck2 TLS 1.2/1.3 lists | verified on OpenWrt ARM64: Go CLI invokes upstream `blockcheck2.sh`, captures log/SUMMARY, and restores service; `example.com` succeeded with TLS 1.2/1.3 without bypass |
| domains (netrogat/custom/substring) | done |
| backups create/list/download/delete/upload | done |
| scopes | done |
| settings (~35) | done |
| supersweep start/status/cancel | done; isolated `NfqwsProbe` is wired by WebAPI |
| update_check | done (`internal/deploy`) |
| recommendations | done (`internal/telemetry`) |

## Стратегии / orchestra

| Функция | Статус |
| --- | --- |
| locked.tsv / locked.manual.tsv / scopes | done |
| blob/sni/mode/clonesize overrides | done |
| auto-sweep | done (`internal/sweep`) |
| supersweep coordinator + progress files | done |
| Изолированный nfqws2 probe | verified on OpenWrt ARM64: quick-profile and SuperSweep API runs used separate qnum 65535 and cleaned probe process/firewall state on completion/cancel |
| YouTube media checks inside SuperSweep | partial: URLs are prepared before the isolated probe and media verdicts gate winners; router YT phase passed 44/44, but Googlevideo phase through primary WAN timed out |
| Googlevideo seed provenance | fixed: only learned/legacy rows count as transfer evidence; successful probes write `learned`, no-probe defaults do not create locks |

## Сеть / платформа

| Функция | Статус |
| --- | --- |
| Platform detect VPS/OpenWRT/Entware | done |
| Firewall client-scope | done |
| wssize-ramp rules + custom.d | done |
| SuperSweep marks/chains | done |
| Googlevideo central list + collector | done (central cache, local queue, telemetry uploader wired; router verification pending) |
| DNS sniffer C binary orchestration | done (режим sniffer/ubus, без dnsmasq-full; reconnect manager) |
| Provider ASN detect | done |
| Telemetry consent + send | done (токен из env) |
| Watchdog / strategy-validator daemon | partial (validator templates; Entware nfqws2 respawn — `zator-nfqws-watchdog`) |
| Pre-update backup + post-update verify | done (`CreatePreUpdateBackup` fails closed unless both product archive and `bin/zator` snapshot succeed; CLI and WebUI abort before applying an update) |
| Config inter-process write-lock | done (`fsutil.WithDirLock` in `Config.SaveTo`) |
| Transactional deploy apply | done (preflight hash verification, per-file undo, rollback errors surfaced; rollback-on-mid-apply failure covered) |
| Transactional backup restore | done (snapshot undo in `RestoreFile`) |
| VPS panels 3x-ui/marzban/wg/3proxy | out of scope для ядра; можно добавить later |
| Интерактивное ANSI-меню | removed (breaking) |

## Данные / Lua (не на Go)

| Компонент | Статус |
| --- | --- |
| Lua zator (`locked.lua`, wssize-ramp, …) | shipped as assets |
| bol-van / nfqws2 / ip2net / mdig | external download |
| Upstream Lua desync API | untouched |
| Server stats Redis (`tools/stats`) | out of scope |

## Ручной QA (роутер / VPS)

- [ ] Ubuntu 22/24: `curl …/zator-launcher.sh | sh`, открыть Web UI, start zapret2
- [ ] Debian cloud VPS: install + check YouTube/Discord
- [ ] OpenWRT: full install/update/reboot, nftables and wssize-ramp order (Go CLI stop/start smoke passed)
- [x] OpenWRT: Go WebUI/profile-2 media probe isolated from live Bash service; quick profile 44/44 and SuperSweep YouTube phase 44/44 completed; Googlevideo WAN phase timed out and was cancelled
- [x] OpenWRT: quick-profile API start/cancel; isolated probe and SuperSweep firewall cleanup
- [x] OpenWRT: quick profile 2 completed all 44 strategies in a temporary ZATOR_ROOT; zero winners, probe/firewall cleanup verified
- [x] OpenWRT ARM64 Go binary, loopback WebUI and status API smoke (temporary `/tmp` roots)
- [x] Router media end-to-end: after reboot with Full WSSIZE, TLS 1.2/1.3 and real signed media ranges succeeded through `AWG_pers`; primary WAN Googlevideo times out and client playback remains unverified
- [x] Blockcheck2 Go CLI launch and SUMMARY creation on router (target TLS checks aborted with DNS code 6; port-check unavailable without ncat)
- [ ] Keenetic Entware: S90, ndm hook, iptables
- [ ] Merlin: firewall-start hook
- [ ] MIPS/arm бинарник стартует, Web UI отвечает, locks пишутся атомарно
- [ ] Offline bundle на устройстве без GitHub
- [ ] Update path: latest.json → apply keep-if-exists
