# Чеклист паритета: bash zator → Go zator

Референс: ветка `feat/googlevideo-fix` репозитория AloofLibra/zator.
Цель: весь управляющий функционал доступен через bootstrap CLI + Web UI.

## Аудит перехода на `zatorGO` (2026-11-15)

| Функция | Bash `feat/googlevideo-fix` | Go `zatorGO` | Пробел / действие |
| --- | --- | --- | --- |
| Vue 3 WebUI и API | Полный интерфейс Zator | Основные экраны/API перенесены; check-контракт расширен для media | Продолжить контрактную проверку сценариев |
| YouTube media | Innertube, подписанные audio/video URL, ограниченная загрузка | Профиль 2 автономно получает URL, проверяет аудио/видео Range и обновляет просроченные ссылки один раз | Успешный media-run на роутере пока не подтверждён |
| Blockcheck2 HTTPS lists | Установка `10-list.sh` и списков TLS 1.2/1.3 в zapret2 перед запуском `blockcheck2.sh` | Списки и z4r loader встроены; установка раскладывает файлы в `$ZAPRET2_ROOT/blockcheck2.d/z4r` | Go CLI/WebUI пока не запускает blockcheck2 и не показывает его summary |
| Быстрый подбор | Однопрофильный/доменный подбор, отмена, применение результата | Есть `AutoSweepProfile` как библиотечный механизм; отдельного API/UI для запуска нет | Реализовать короткий пользовательский сценарий без полного SuperSweep |
| WSSIZE Full/Lite | Full/Lite, SYN/ACK, window scale, nftables/iptables, Googlevideo set, toggles | Backend и contract tests покрывают оба режима, nftables/ipset и выключение без навязывания пользовательского значения | CPU и порядок реальных firewall правил требуют измерения на роутере |
| Авторотация/детекторы | Lua `circular_quality`, success/failure detectors, SLM | Текущие Lua assets сохранены; Go seed пишет provenance, learned-only transfer; validator service доступен | Проверить ложноположительные срабатывания и client-scope на реальном трафике |
| Пользовательские настройки | Профили, blob/SNI/clone, DNS/RST/reassembly/fallback/WG/QUIC/UDP, scopes, provider, telemetry, backup | Существующие WebUI/API/config backend и contract tests сохранены; требуется сквозное применение на платформах | Полный settings round-trip на роутере ещё не проверен |
| Аварийный CLI | Статус, ошибки, службы, сброс локов, restore/rollback | `status`, `service`, `locks clear -y` (создаёт backup), `restore` (smart), `rollback` доступны без браузера | Лог службы зависит от platform backend; CLI пока показывает сохранённые ошибки Zator, не системный journal |
| SuperSweep | Фазы, media probe, локи, прогресс и изолированный transport | URLs подготавливаются до старта `NfqwsProbe`; media verdict участвует в выборе победителя | Проверить полный цикл на роутере |
| Googlevideo collector/seed | UBus/AF_PACKET, queue/upload, центральный IP-set и проверка/apply стратегий | A/CNAME parser, TTL/public-IP checks, bounded RAM dedupe, tmpfs queue, central feed, UBus/AF_PACKET reconnect; consent/token-gated 3-hour upload; new IPs merge incrementally into nft set/ipset; seed provenance fixed | Поведение на Keenetic и доставка наблюдений серверу требуют platform E2E |
| Установка, обновление, откат | Bash installer и rollback | CLI, offline/update и безопасное сохранение данных есть в Go | Повторить install/update/rollback сценарии на стенде |
| OpenWrt/Keenetic | Проверены отдельные платформенные сценарии | Платформенные backend есть; ARM64 loopback smoke пройден | Проверить службы/firewall без вмешательства в рабочую установку |
| Нагрузка слабого роутера | Измерения зависят от устройства | Замеры CPU/RSS отсутствуют | Измерить простой и явные проверки на целевом устройстве |

На тестовом OpenWrt 25.12.4/ARM64 временный Go WebUI ответил на loopback; профиль 2 получил timeout от Googlevideo, а контрольный YouTube ответил. Поэтому реальную медиазагрузку на роутере пока нельзя считать проверенной. Текущая Bash-служба и её firewall не менялись.

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
| Profile 2 YouTube media check | partial: Innertube resolves signed audio/video URLs, checks bounded ranges and refreshes stale HTTP URLs once; router media success remains unverified |
| Blockcheck2 TLS 1.2/1.3 lists | partial: upstream z4r loader and test lists are embedded and installed into the zapret2 tree; CLI launch/summary is not wired |
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
| Изолированный nfqws2 probe | done (`internal/sweep.NfqwsProbe`, WebAPI wiring); router run still pending |
| YouTube media checks inside SuperSweep | partial: URLs are prepared before the isolated probe; media verdicts gate winners; full router run remains unverified |
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
| Pre-update backup + post-update verify | done (`CreatePreUpdateBackup`, `VerifyAfterUpdate`) |
| Config inter-process write-lock | done (`fsutil.WithDirLock` in `Config.SaveTo`) |
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
- [ ] OpenWRT: procd unit, nftables, wssize-ramp
- [ ] OpenWRT: Go WebUI/profile-2 media probe isolated from live Bash service
- [x] OpenWRT ARM64 Go binary, loopback WebUI and status API smoke (temporary `/tmp` roots)
- [ ] Router media end-to-end: Googlevideo TLS timed out while the YouTube control check answered; Innertube/media transfer was not reached
- [ ] Blockcheck2 launch through Go CLI and summary availability on router
- [ ] Keenetic Entware: S90, ndm hook, iptables
- [ ] Merlin: firewall-start hook
- [ ] MIPS/arm бинарник стартует, Web UI отвечает, locks пишутся атомарно
- [ ] Offline bundle на устройстве без GitHub
- [ ] Update path: latest.json → apply keep-if-exists
