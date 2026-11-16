# Чеклист паритета: bash zator → Go zator

Референс: ветка `feat/googlevideo-fix` репозитория AloofLibra/zator.
Цель: весь управляющий функционал доступен через bootstrap CLI + Web UI.

## Аудит перехода на `zatorGO` (2026-11-16)

| Функция | Bash `feat/googlevideo-fix` | Go `zatorGO` | Пробел / действие |
| --- | --- | --- | --- |
| Vue 3 WebUI и API | Полный интерфейс Zator | Основные экраны/API перенесены; Vite build, vue-tsc и npm audit проходят; роутер отдаёт assets с совпавшими SHA-256 | Продолжить контрактную проверку сценариев |
| YouTube media | Innertube, подписанные audio/video URL, ограниченная загрузка | Profile 2 и SuperSweep получают signed URL, проверяют audio 256 KiB, video 1 MiB и video middle 2 MiB; валидируют 206, точный Content-Range, MIME и объём. SuperSweep повторяет всю тройку отдельно по TLS 1.2 и 1.3; CDN redirect ограничен двумя подписанными Googlevideo URL. Resolver сохраняет длины и MIME форматов; DNS/API/подпись/HTTP-валидность дают unknown, обрыв тела и сетевой сбой классифицируются отдельно | На тестовом роутере новая ARM64-медиапроба через `AWG_pers` прошла все 6 точных Range-запросов; Go Profile 2 теперь также прошёл через основной WAN: TLS 1.2/1.3 и 3 точных media ranges. Клиентское воспроизведение ещё не проверено |
| Blockcheck2 HTTPS lists | Установка `10-list.sh` и списков TLS 1.2/1.3 в zapret2 перед запуском `blockcheck2.sh` | Go CLI передаёт upstream имя хоста; на OpenWrt ARM64 `example.com` прошёл TLS 1.2 и TLS 1.3 (`working without bypass`), SUMMARY сохранён, procd-служба восстановлена | Отдельный port-check недоступен без ncat; DPI-blocked hostname требует отдельного тестового окна/цели |
| Быстрый подбор | Однопрофильный/доменный подбор, отмена, применение результата | На OpenWrt ARM64 быстрый Profile 2 завершил 44/44 стратегии в изолированном probe: 7 `OK`, 3 предупреждения, 34 отказа; выбран и применён победитель 30. Обычная Go-медиапроба после применения также прошла по основной очереди | Применение победителя проверено для Profile 2; WSSIZE ещё требуется согласовать с параметрами изолированного probe |
| WSSIZE Full/Lite | Full/Lite, SYN/ACK, window scale, nftables/iptables, Googlevideo set, toggles | Go runtime в `/opt/zator` работает через procd. Full включён (`wsize=1:scale=2:open=1`); после применения quick-profile победителя 30 основной Go Profile 2 вернул все media ranges, а WAN capture показал `win 1, wscale 2` на media SYN | Отдельная задача: применять к изолированному probe те же Full/Lite, wsize, scale, open и Googlevideo IP-set, что и к основной очереди; затем повторно проверить профильный SuperSweep. Длительная потоковая нагрузка не измерена |
| Авторотация/детекторы | Lua `circular_quality`, success/failure detectors, SLM | Текущие Lua assets сохранены; Go seed пишет provenance, learned-only transfer; validator service доступен | Проверить ложноположительные срабатывания и client-scope на реальном трафике |
| Пользовательские настройки | Профили, blob/SNI/clone, DNS/RST/reassembly/fallback/WG/QUIC/UDP, scopes, provider, telemetry, backup | Существующие WebUI/API/config backend и contract tests сохранены; требуется сквозное применение на платформах | Полный settings round-trip на роутере ещё не проверен |
| Аварийный CLI | Статус, ошибки, службы, сброс локов, restore/rollback | `status`, `service`, `locks clear -y` (создаёт backup), `restore` (smart), `rollback` доступны без браузера | Лог службы зависит от platform backend; CLI пока показывает сохранённые ошибки Zator, не системный journal |
| SuperSweep | Фазы, media probe, локи, прогресс и изолированный transport | Предыдущий полный многопрофильный прогон прошёл YouTube 44/44, но был отменён на Googlevideo после 6 timeout. В новом quick Profile 2 завершены 44/44 изолированных стратегий: 7 media OK, 3 TLS-only warnings, 34 fail; стратегия 30 применена, обычная медиапроба затем прошла. Probe/qnum 65535 очищены, основной lock сохранён/проверен | Полный многопрофильный прогон ещё не повторён после исправления blob; отдельная задача — передавать в qnum 65535 те же параметры WSSIZE, что и в основную очередь |
| Googlevideo collector/seed | UBus/AF_PACKET, queue/upload, центральный IP-set и проверка/apply стратегий | A/CNAME parser, TTL/public-IP checks, bounded RAM dedupe, tmpfs queue, central feed, UBus/AF_PACKET reconnect; consent/token-gated 3-hour upload; new IPs merge incrementally into nft set/ipset. Background seed transfers only a learned/legacy majority from askey 2/default scope; rows stay marked `seed`, state is persisted before live TSV, and missing majority with no probe remains `unknown` | Поведение на Keenetic и доставка наблюдений серверу требуют platform E2E |
| Установка, обновление, откат | Bash installer и rollback | CLI/offline/update; update требует product backup + бинарный snapshot; полный rollback config/lists/bin покрыт тестом; uninstall прекращается при ошибке stop/disable. На тестовом OpenWrt ARM64 проверены legacy offline apply с целевым ARM64 бинарником и текущий vendor-бинарник с SHA-256; оба сценария прошли post-update verify и rollback с восстановлением бинарника. Пользовательский список и custom blob сохранились. Clean/repeat install, uninstall с сохранением данных, reinstall и явный purge прошли в отдельном `/tmp` runtime. Проверены также GitHub binary fallback и local-vendor bootstrap через launcher | Собственный релиз не опубликован; online `latest.json` archive path и release rollback требуют проверки после публикации |
| OpenWrt/Keenetic | Проверены отдельные платформенные сценарии | На тестовом OpenWrt ARM64 WebUI и zapret2 поднялись после reboot; offline update/rollback оставил WebUI HTTP 200 и рабочий qnum 300 с WSSIZE Full. Новые тесты установки, двух offline update путей, rollback и local-vendor launcher запускались только в изолированных корнях, без изменения рабочего сервиса | Собственный online release E2E и Keenetic E2E не выполнены |
| Размер бинарника и память | Измерения зависят от устройства | На ARM64 обычная сборка 19,021,071 B; `-s -w -buildid=` — 13,893,756 B (−27%). Одинаковый временный WebUI runtime после запроса API: RSS 14,276 vs 14,412 KiB, 8 threads у обоих. Release workflow уже задаёт `-s -w`; UPX на хосте отсутствует | Экономия flash от strip не уменьшила RSS в пределах этого замера. Проверить реальный release-пакет и упаковку/сжатие (включая RAM/CPU/старт) на целевом роутере; затем 128-MiB/MIPS |
| Нагрузка слабого роутера | Измерения зависят от устройства | На тестовом OpenWrt ARM64 после обновления WebUI: RSS zator 15.2 MiB, 7 threads; system idle 90% в двух снимках с интервалом 5 секунд | Устройство имеет 496716 KiB RAM; это не целевой 128-MiB роутер. Collector и watchdog в idle-замере отдельно не измерялись |

На тестовом OpenWrt 25.12.4/ARM64 текущая ветка Go 0.1.39 установлена в штатный `/opt/zator`; `zator-webui`, `zator-strategy-validator` и `zapret2` показывают running через procd. После reboot все три службы поднялись автоматически, WebUI ответил HTTP 200. Bootstrap зарегистрировал upstream `/etc/init.d/zapret2`; автозапуск содержит только текущие `S21/K10` links, без дублей от старого Go unit. Go API Profile 2 после reboot на основном WAN вернул TLS 1.2/1.3 и `Media OK`: HTTP 206, audio 262144 B, video 1048576 B и middle video 2097152 B. Quick-profile 2 затем завершил изолированные 44 стратегии, выбрал и применил стратегию 30 (7 OK, 3 TLS-only warnings, 34 failures); штатная медиапроба после применения повторно прошла все три диапазона. Основной lock теперь 30; отдельный nfqws2 probe и его правила после завершения очищены. Signed video ID: `BPk1uJ4txPc`. Одновременный `tcpdump` на `wan` зафиксировал SYN к `188.244.8.77:443` от Go-медиапробы с `win 1, wscale 2`; контрольный HTTPS SYN имел `win 64240, wscale 6`. API настроек сообщил `enabled=true`, `mode=full`, `scale=2`, 949 свежих Googlevideo IP. В конфиге больше нет ссылок на временный `/opt/zator-go`; 116 blob-файлов, custom TLS blob, пользовательские списки и profile lock сохранились. Go bootstrap теперь регистрирует upstream procd init, если файл есть в zapret2, но не подключён в `/etc/init.d`, очищает устаревшие autostart links и включает правильную службу. Небольшой Go status matcher также исправлен: аргумент с текстом `strategy-validator` больше не выдаёт nfqws2 и текущую команду за validator. Медиапроба через `AWG_pers` (`10.8.1.14`) ранее также получила точные HTTP 206. Полный SuperSweep использует отдельный qnum 65535; его прежние Googlevideo timeout не являются отдельной валидацией WSSIZE. Default TLS/download timeout — 10 с, TCP connect timeout — 3 с. Ранее полный многопрофильный SuperSweep был отменён после 6/44 Googlevideo timeout; последующий профильный quick run завершился 44/44 и применил стратегию 30, но полный многопрофильный прогон ещё нужно повторить. Онлайн-релиз по `latest.json`, клиентское воспроизведение, длительная потоковая нагрузка и Keenetic остаются незавершёнными. Тестовый router имеет 496716 KiB RAM; CPU/RSS после финальной Go-установки не перемерялись.

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
| Profile 2 YouTube media check | verified on OpenWrt ARM64: applied strategy 30 returned TLS 1.2/1.3 and real HTTP 206 media ranges (audio 256 KiB, video 1 MiB, middle 2 MiB); WAN capture during the Go API test showed the media SYN with `win 1, wscale 2`; client playback is unverified |
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
| Изолированный nfqws2 probe | verified on OpenWrt ARM64: quick-profile and SuperSweep API runs used separate qnum 65535 and cleaned probe process/firewall state on completion/cancel; this isolated path is not the proof of WSSIZE |
| YouTube media checks inside SuperSweep | partial: quick Profile 2 probe completed 44/44 (7 media OK, 3 TLS-only warnings, 34 failures) and applied strategy 30; the ordinary Profile 2 check then passed on the primary queue. Isolated qnum 65535 still needs the same WSSIZE parameters as the primary queue; the full multi-phase run remains unverified |
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
- [x] OpenWRT ARM64: offline bundle update + post-update verify + rollback; source binary restored and config/lists/lock hashes preserved; WebUI and nfqws2 remained running
- [x] OpenWRT ARM64: clean/repeat install, обычный uninstall с сохранением пользовательских данных, reinstall и явный purge в отдельном `/tmp` корне
- [x] OpenWRT ARM64: offline launcher взял бинарник из `vendor/`, сверил checksum и установил WebUI без доступа к GitHub (`-zapret=false`, службы пропущены)
- [x] OpenWRT ARM64: legacy и current offline update взяли ARM64 бинарник, сохранили список/blob и прошли rollback с восстановлением бинарника
- [x] OpenWRT ARM64: online GitHub binary fallback smoke (`Koviand/zatorGO v0.1.39`) в изолированном runtime + post-update verify
- [ ] OpenWRT: `latest.json` archive asset от релиза ветки `zatorGOtest` + keep-if-exists apply + reboot, nftables and wssize-ramp order (Go CLI stop/start smoke passed)
- [x] OpenWRT ARM64: Go quick Profile 2 through WebUI completed isolated Googlevideo phase 44/44 (7 OK, 3 TLS-only warnings, 34 failures), applied strategy 30; normal Profile 2 on the primary queue then passed all media ranges; isolated-probe WSSIZE parity remains open
- [x] OpenWRT: quick-profile API start/cancel; isolated probe and SuperSweep firewall cleanup
- [x] OpenWRT: quick profile 2 completed all 44 strategies in a temporary ZATOR_ROOT; zero winners, probe/firewall cleanup verified
- [x] OpenWRT ARM64 Go binary, loopback WebUI and status API smoke (temporary `/tmp` roots)
- [x] Router media end-to-end: after reboot with Full WSSIZE, Go Profile 2 passed TLS 1.2/1.3 and signed media ranges on the primary WAN; after quick-profile applied strategy 30, the check again passed; WAN capture showed `win 1, wscale 2` on media SYN and ordinary window scale on control HTTPS; client playback remains unverified
- [x] Blockcheck2 Go CLI launch and SUMMARY creation on router (target TLS checks aborted with DNS code 6; port-check unavailable without ncat)
- [ ] Keenetic Entware: S90, ndm hook, iptables
- [ ] Merlin: firewall-start hook
- [ ] MIPS/arm бинарник стартует, Web UI отвечает, locks пишутся атомарно
- [ ] Offline bundle на устройстве без GitHub
- [ ] Update path: published `latest.json` → release apply with keep-if-exists on a router
