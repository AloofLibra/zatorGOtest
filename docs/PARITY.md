# Чеклист паритета: bash zator → Go zator

Референс: ветка `feat/googlevideo-fix` репозитория AloofLibra/zator.
Цель: весь управляющий функционал доступен через bootstrap CLI + Web UI.

## Аудит перехода на `zatorGO` (2026-11-15)

| Функция | Bash `feat/googlevideo-fix` | Go `zatorGO` | Пробел / действие |
| --- | --- | --- | --- |
| Vue 3 WebUI и API | Полный интерфейс Zator | Основные экраны/API перенесены; check-контракт расширен для media | Продолжить контрактную проверку сценариев |
| YouTube media | Innertube, подписанные audio/video URL, ограниченная загрузка | Профиль 2 получает URL автономно; на OpenWrt в 44-стратегийном изолированном прогоне resolver=`ready`, но все 44 media TLS-пробы к CDN завершились timeout, медиа-поток не подтверждён | Требуется выяснить сетевой timeout к CDN; положительный real-media результат отсутствует |
| Blockcheck2 HTTPS lists | Установка `10-list.sh` и списков TLS 1.2/1.3 в zapret2 перед запуском `blockcheck2.sh` | Go CLI передаёт upstream имя хоста; на OpenWrt ARM64 `example.com` прошёл TLS 1.2 и TLS 1.3 (`working without bypass`), SUMMARY сохранён, procd-служба восстановлена | Отдельный port-check недоступен без ncat; DPI-blocked hostname требует отдельного тестового окна/цели |
| Быстрый подбор | Однопрофильный/доменный подбор, отмена, применение результата | WebUI запускает один профиль и адрес через существующий coordinator; на OpenWrt ARM64 проверены API start/cancel, отдельный probe и cleanup; профиль 2 закончил все 44 стратегии, прогресс был доступен, зелёных кандидатов не нашлось | Применение зелёного победителя не проверено, так как реальная цель Googlevideo недоступна из роутера |
| WSSIZE Full/Lite | Full/Lite, SYN/ACK, window scale, nftables/iptables, Googlevideo set, toggles | Backend и contract tests покрывают оба режима, nftables/ipset и выключение без навязывания пользовательского значения; миграция legacy config добавляет отсутствующий WSSIZE Googlevideo-профиль и Lua init, при первом переходе включает Lite, сохраняя последующее явное выключение; smoke на роутере с копией его legacy config сгенерировал активные Lite args и проверил Lua asset | Пакетные данные и runtime правила не запускались на трафике; CPU и порядок реальных firewall правил требуют отдельной проверки |
| Авторотация/детекторы | Lua `circular_quality`, success/failure detectors, SLM | Текущие Lua assets сохранены; Go seed пишет provenance, learned-only transfer; validator service доступен | Проверить ложноположительные срабатывания и client-scope на реальном трафике |
| Пользовательские настройки | Профили, blob/SNI/clone, DNS/RST/reassembly/fallback/WG/QUIC/UDP, scopes, provider, telemetry, backup | Существующие WebUI/API/config backend и contract tests сохранены; требуется сквозное применение на платформах | Полный settings round-trip на роутере ещё не проверен |
| Аварийный CLI | Статус, ошибки, службы, сброс локов, restore/rollback | `status`, `service`, `locks clear -y` (создаёт backup), `restore` (smart), `rollback` доступны без браузера | Лог службы зависит от platform backend; CLI пока показывает сохранённые ошибки Zator, не системный journal |
| SuperSweep | Фазы, media probe, локи, прогресс и изолированный transport | URLs подготавливаются до старта `NfqwsProbe`; media verdict участвует в выборе победителя | Проверить полный цикл на роутере |
| Googlevideo collector/seed | UBus/AF_PACKET, queue/upload, центральный IP-set и проверка/apply стратегий | A/CNAME parser, TTL/public-IP checks, bounded RAM dedupe, tmpfs queue, central feed, UBus/AF_PACKET reconnect; consent/token-gated 3-hour upload; new IPs merge incrementally into nft set/ipset; seed provenance fixed | Поведение на Keenetic и доставка наблюдений серверу требуют platform E2E |
| Установка, обновление, откат | Bash installer и rollback | CLI, offline/update и безопасное сохранение данных есть в Go | Повторить install/update/rollback сценарии на стенде |
| OpenWrt/Keenetic | Проверены отдельные платформенные сценарии | На тестовом OpenWrt ARM64 Go CLI через procd остановил/запустил zapret2; nftables правила восстановлены штатным init; ARM64 loopback smoke пройден | Повторить полный install/update/reboot и WSSIZE порядок правил; Keenetic E2E не выполнен |
| Нагрузка слабого роутера | Измерения зависят от устройства | На тестовом OpenWrt API-only ARM64 процесс: RSS 8.8 MiB, 6 threads; system idle 95–97% в двух 5-секундных окнах | Устройство имеет 496716 KiB RAM; тест не запускал collector/watchdog. Повторить полный idle и явную проверку на целевом слабом роутере |

На тестовом OpenWrt 25.12.4/ARM64 временный Go WebUI ответил на loopback; profile-2 check получил Googlevideo timeout, тогда как контрольный YouTube и 64 KiB download ответили. Изолированный quick profile 2 проверил все 44 стратегии; media resolver вернул `ready`, однако каждый кандидат дал TLS timeout к CDN, поэтому real-media успеха нет. Независимый `curl` к тому же Googlevideo host истёк на connect timeout 5 с, а запрос к YouTube завершился HTTP 404 после TLS за 0.168 с — это подтверждает разницу маршрута CDN и control host, но не доказывает причину блокировки. После завершения probe-процесс с очередью 65535 и правила `z2r_supersweep`/`Z2R_SS` отсутствовали; результаты лежали во временном `ZATOR_ROOT`, а `/etc/init.d/zapret2` оставался `running`. Первые запуски Blockcheck2 завершались с `curl code 6`: CLI передавал полный URL вместо hostname в `DOMAINS`. После исправления `example.com` прошёл TLS 1.2/1.3 без обхода; SUMMARY сохранён и zapret2 восстановлен. WSSIZE smoke запустил Go `ApplyZapretRuntime` на временной копии текущего router config и отдельных `/tmp` roots: legacy config мигрировал, профиль 2 получил `wssize_ramp:mode=lite` без `--skip` на этом правиле, Lua asset был на месте. Рабочий `/opt/zapret2/config` оставлен Bash-версией без WSSIZE; service продолжил работать. Default TLS probe timeout увеличен с 5 до 10 с, TCP connect timeout остался 3 с. Подходящего `ncat` на роутере нет, port-check не выполнялся.

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
| Profile 2 YouTube media check | partial: Innertube resolves signed audio/video URLs, checks bounded ranges and refreshes stale HTTP URLs once; router media success remains unverified |
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
- [ ] OpenWRT: full install/update/reboot, nftables and wssize-ramp order (Go CLI stop/start smoke passed)
- [ ] OpenWRT: Go WebUI/profile-2 media probe isolated from live Bash service (44 strategies tried; all CDN TLS checks timed out, real media remains unverified)
- [x] OpenWRT: quick-profile API start/cancel; isolated probe and SuperSweep firewall cleanup
- [x] OpenWRT: quick profile 2 completed all 44 strategies in a temporary ZATOR_ROOT; zero winners, probe/firewall cleanup verified
- [x] OpenWRT ARM64 Go binary, loopback WebUI and status API smoke (temporary `/tmp` roots)
- [ ] Router media end-to-end: Googlevideo TLS timed out while the YouTube control check answered; Innertube/media transfer was not reached
- [x] Blockcheck2 Go CLI launch and SUMMARY creation on router (target TLS checks aborted with DNS code 6; port-check unavailable without ncat)
- [ ] Keenetic Entware: S90, ndm hook, iptables
- [ ] Merlin: firewall-start hook
- [ ] MIPS/arm бинарник стартует, Web UI отвечает, locks пишутся атомарно
- [ ] Offline bundle на устройстве без GitHub
- [ ] Update path: latest.json → apply keep-if-exists
