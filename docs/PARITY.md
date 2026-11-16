# Чеклист паритета: bash zator → Go zator

Референс: ветка `feat/googlevideo-fix` репозитория AloofLibra/zator.
Цель: весь управляющий функционал доступен через bootstrap CLI + Web UI.

## Аудит перехода на `zatorGO` (2026-11-16)

| Функция | Bash `feat/googlevideo-fix` | Go `zatorGO` | Пробел / действие |
| --- | --- | --- | --- |
| Vue 3 WebUI и API | Полный интерфейс Zator | Основные экраны/API перенесены; Vite build, vue-tsc и npm audit проходят; роутер отдаёт assets с совпавшими SHA-256 | Продолжить контрактную проверку сценариев |
| YouTube media | Innertube, подписанные audio/video URL, ограниченная загрузка | Profile 2 и SuperSweep получают signed URL, проверяют audio 256 KiB, video 1 MiB и video middle 2 MiB; валидируют 206, точный Content-Range, MIME и объём. SuperSweep повторяет всю тройку отдельно по TLS 1.2 и 1.3; CDN redirect ограничен двумя подписанными Googlevideo URL. Resolver сохраняет длины и MIME форматов; DNS/API/подпись/HTTP-валидность дают unknown, обрыв тела и сетевой сбой классифицируются отдельно | На тестовом роутере новая ARM64-медиапроба через `AWG_pers` прошла все 6 точных Range-запросов; Go Profile 2 теперь также прошёл через основной WAN: TLS 1.2/1.3 и 3 точных media ranges. Клиентское воспроизведение ещё не проверено |
| Blockcheck2 HTTPS lists | Установка `10-list.sh` и списков TLS 1.2/1.3 в zapret2 перед запуском `blockcheck2.sh` | 2026-11-16 на OpenWrt 25.12.4 ARM64 Go CLI запустил upstream Blockcheck2 для `google.com`; TLS 1.2/1.3 завершились `working without bypass`, SUMMARY сохранён, procd-служба восстановлена; после прогона nfqws2 работал, NFQUEUE qnum 65535 отсутствовал | Port-check пропущен: ncat не установлен. TLS результат подтверждает сквозной вызов, но не работу DPI bypass; целевой обходной домен требует отдельной проверки |
| Быстрый подбор | Однопрофильный/доменный подбор, отмена, применение результата | Исторический запуск до включения WSSIZE в isolated probe завершил 44/44 (7 `OK`, 3 предупреждения, 34 отказа), применил стратегию 30; обычная медиапроба прошла на основной очереди. Последний запуск с isolated WSSIZE завершил 44/44 (10 OK, 3 предупреждения, 31 отказ), выбрана стратегия 30 | Bash reference отключает WSSIZE в probe и использует curl, поэтому результаты неравноценны |
| WSSIZE Full/Lite | Full/Lite, SYN/ACK, window scale, nftables/iptables, Googlevideo set, toggles | Go runtime в `/opt/zator` работает через procd. Full включён (`wsize=1:scale=2:open=1`). Код ставит WSSIZE mark для Googlevideo до isolated qnum 65535; WAN capture именно изолированной пробы показал SYN `win 1, wscale 2` | Последний изолированный quick Profile 2 завершил 44/44, WSSIZE Full активен; длительная потоковая нагрузка не измерена |
| Авторотация/детекторы | Lua `circular_quality`, success/failure detectors, SLM | Текущие Lua assets сохранены; Go seed пишет provenance, learned-only transfer; validator service доступен | Проверить ложноположительные срабатывания и client-scope на реальном трафике |
| Пользовательские настройки | Профили, blob/SNI/clone, DNS/RST/reassembly/fallback/WG/QUIC/UDP, scopes, provider, telemetry, backup | Существующие WebUI/API/config backend и contract tests сохранены; требуется сквозное применение на платформах | Полный settings round-trip на роутере ещё не проверен |
| Аварийный CLI | Статус, ошибки, службы, сброс локов, restore/rollback | `status`, `service`, `locks clear -y` (создаёт backup), `restore` (smart), `rollback` доступны без браузера | Лог службы зависит от platform backend; CLI пока показывает сохранённые ошибки Zator, не системный journal |
| SuperSweep | Фазы, media probe, локи, прогресс и изолированный transport | Исправлен конфиг isolated probe: вместо полного runtime `NFQWS2_OPT` используются общий TLS-шаблон и только выбранный профиль. Private orchestra получает snapshot четырёх menu-16 override-файлов; на роутере проверены profile 2 `clone` и SNI `www.google.com`. Quick Profile 2 с WSSIZE Full завершил 44/44: 10 OK, 3 предупреждения, 31 отказ; зелёные 3, 16, 17, 19, 21, 22, 24, 30, 34, 44; жёлтая 10; выбрана стратегия 30. После завершения qnum 65535 и правила SuperSweep очищены, службы работают | Результат не совпадает с Bash (у него WSSIZE в isolated probe принудительно отключён, плюс другой TLS-клиент). Clone mode переопределяет default fake blob aliases `maxru`/`fake_default_tls`, а специальные blobs вроде `clone_vk` и `clone_hcaptcha` сохраняются |
| Googlevideo collector/seed | UBus/AF_PACKET, queue/upload, центральный IP-set и проверка/apply стратегий | На OpenWrt 25.12.4 ARM64 снято реальное `dnsmasq.dns` событие `rr1.sn-h5gtov-n8vl.googlevideo.com → 188.244.8.76`; CNAME-форма нормализуется в media URL host `rr1---sn-h5gtov-n8vl.googlevideo.com`, Domains API возвращает `source=dns` и IP. Go UBus parser для `rrN.sn-…` покрыт runtime-пойманным fixture. Найдено и устранено двойное потребление события C+Go: при активном C collector по PID+cmdline Go не запускает второй listener/uploader; после рестарта WebUI остался один активный UBus subscriber. Seed не создаёт lock без majority/probe | Проверить Keenetic AF_PACKET, Go-only fallback без C collector, обновление central feed при недоступности и consent-gated upload E2E |
| Установка, обновление, откат | Bash installer и rollback | CLI/offline/update; update требует product backup + бинарный snapshot; полный rollback config/lists/bin покрыт тестом; uninstall прекращается при ошибке stop/disable. На тестовом OpenWrt ARM64 проверены legacy offline apply с целевым ARM64 бинарником и текущий vendor-бинарник с SHA-256; оба сценария прошли post-update verify и rollback с восстановлением бинарника. Пользовательский список и custom blob сохранились. Clean/repeat install, uninstall с сохранением данных, reinstall и явный purge прошли в отдельном `/tmp` runtime. Проверены также GitHub binary fallback и local-vendor bootstrap через launcher | Собственный релиз не опубликован; online `latest.json` archive path и release rollback требуют проверки после публикации |
| OpenWrt/Keenetic | Проверены отдельные платформенные сценарии | На тестовом OpenWrt ARM64 WebUI и zapret2 поднялись после reboot; offline update/rollback оставил WebUI HTTP 200 и рабочий qnum 300 с WSSIZE Full. Новые тесты установки, двух offline update путей, rollback и local-vendor launcher запускались только в изолированных корнях, без изменения рабочего сервиса | Собственный online release E2E и Keenetic E2E не выполнены |
| Размер бинарника и память | Измерения зависят от устройства | ARM64: обычная сборка 19,021,071 B; `-s -w -buildid=` — 13,893,756 B (−27%). UPX 5.3.0 (`-9`) сократил stripped до 4,825,416 B (`upx -t` OK; SHA инструмента совпал с GitHub release). Повторный runtime-замер: WebUI `VmHWM` default 12,652 KiB, stripped 12,648 KiB, UPX 16,452 KiB (+3.7 MiB); readiness (5 запусков) 79.1, 80.1 и 383.7 ms соответственно. CLI `version` (10 запусков) — 14.8, 14.5 и 316.2 ms. На этом OpenWrt ядре нет `/proc/<pid>/smaps` и `smaps_rollup`, поэтому PSS недоступен. Release workflow уже применяет `-s -w` | Повторить PSS на ядре с `CONFIG_PROC_PAGE_MONITOR`; отдельно сравнить точную команду `--best --lzma`, Garble `-tiny` + UPX, доли зависимостей и embedded assets, release-пакет и целевые MIPS/128-MiB устройства; решение принимать по flash/RAM/CPU/startup/диагностике |
| Нагрузка слабого роутера | Измерения зависят от устройства | На тестовом OpenWrt ARM64 после обновления WebUI: RSS zator 15.2 MiB, 7 threads; system idle 90% в двух снимках с интервалом 5 секунд | Устройство имеет 496716 KiB RAM; это не целевой 128-MiB роутер. Collector и watchdog в idle-замере отдельно не измерялись |

На тестовом OpenWrt 25.12.4/ARM64 текущая ветка Go 0.1.39 установлена в штатный `/opt/zator`; `zator-webui`, `zator-strategy-validator` и `zapret2` показывают running через procd. После reboot все три службы поднялись автоматически, WebUI ответил HTTP 200. Bootstrap зарегистрировал upstream `/etc/init.d/zapret2`; автозапуск содержит только текущие `S21/K10` links, без дублей от старого Go unit. Go API Profile 2 после reboot на основном WAN вернул TLS 1.2/1.3 и `Media OK`: HTTP 206, audio 262144 B, video 1048576 B и middle video 2097152 B. Ранее quick-profile 2 завершил 44 стратегии, выбрал и применил стратегию 30 (7 OK, 3 TLS-only warnings, 34 failures); это исторический запуск до последнего включения WSSIZE в isolated probe. Последний запуск с WSSIZE завершил 44/44 (10 OK, 3 предупреждения, 31 отказ), применил стратегию 30; probe и firewall cleanup подтверждены. Основной lock теперь 30; отдельный nfqws2 probe и его правила после завершения очищены. Signed video ID: `BPk1uJ4txPc`. Одновременный `tcpdump` на `wan` зафиксировал SYN к `188.244.8.77:443` от Go-медиапробы с `win 1, wscale 2`; контрольный HTTPS SYN имел `win 64240, wscale 6`. API настроек сообщил `enabled=true`, `mode=full`, `scale=2`, 949 свежих Googlevideo IP. В конфиге больше нет ссылок на временный `/opt/zator-go`; 116 blob-файлов, custom TLS blob, пользовательские списки и profile lock сохранились. Go bootstrap теперь регистрирует upstream procd init, если файл есть в zapret2, но не подключён в `/etc/init.d`, очищает устаревшие autostart links и включает правильную службу. Небольшой Go status matcher также исправлен: аргумент с текстом `strategy-validator` больше не выдаёт nfqws2 и текущую команду за validator. Медиапроба через `AWG_pers` (`10.8.1.14`) ранее также получила точные HTTP 206. Полный SuperSweep использует отдельный qnum 65535; его прежние Googlevideo timeout не являются отдельной валидацией WSSIZE. Default TLS/download timeout — 10 с, TCP connect timeout — 3 с. Ранее полный многопрофильный SuperSweep был отменён после 6/44 Googlevideo timeout; последующий профильный quick run завершился 44/44 и применил стратегию 30, но полный многопрофильный прогон ещё нужно повторить. Онлайн-релиз по `latest.json`, клиентское воспроизведение, длительная потоковая нагрузка и Keenetic остаются незавершёнными. Тестовый router имеет 496716 KiB RAM; CPU/RSS после финальной Go-установки не перемерялись.

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
| Blockcheck2 TLS 1.2/1.3 lists | verified on OpenWrt 25.12.4 ARM64 (2026-11-16): Go CLI invokes upstream `blockcheck2.sh` for `google.com`, captures log/SUMMARY, and restores service; TLS 1.2/1.3 both report `working without bypass`; ncat missing, so port-check unavailable; nfqws2 running and qnum 65535 absent after completion |
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
| YouTube media checks inside SuperSweep | partial: Go media transport matches Bash HTTP/1.1, UA, and media timeouts; isolated NFQWS2 uses one selected profile and snapshots menu-16 override files. Router run with WSSIZE Full and profile 2 clone mode completed 44/44 (10 OK, 3 warnings, 31 failures) and applied strategy 30. Bash/Go totals differ and are not a fair WSSIZE comparison: Bash disables WSSIZE in its isolated queue; Go uses it. The Go client also has a different TLS fingerprint from curl |
| Googlevideo seed provenance | fixed: only learned/legacy rows count as transfer evidence; successful probes write `learned`, no-probe defaults do not create locks |

## Сеть / платформа

| Функция | Статус |
| --- | --- |
| Platform detect VPS/OpenWRT/Entware | done |
| Firewall client-scope | done |
| wssize-ramp rules + custom.d | done |
| SuperSweep marks/chains | done |
| Googlevideo central list + collector | partial: OpenWrt ARM64 DNS event, CNAME normalization, Go UBus parser, host/IP API mapping and single-listener ownership verified; Keenetic AF_PACKET, Go-only fallback, feed outage and upload E2E remain |
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
- [x] OpenWRT ARM64: isolated probe uses selected-profile NFQWS2 args and snapshots menu-16 blob/clone override files; quick Profile 2 with WSSIZE Full and profile 2 clone+SNI overrides completed 44/44 (10 OK, 3 warnings, 31 failures), selected strategy 30; queue/firewall cleanup verified
- [x] OpenWRT: quick-profile API start/cancel; isolated probe and SuperSweep firewall cleanup
- [x] OpenWRT: quick profile 2 completed all 44 strategies in a temporary ZATOR_ROOT; zero winners, probe/firewall cleanup verified
- [x] OpenWRT ARM64 Go binary, loopback WebUI and status API smoke (temporary `/tmp` roots)
- [x] Router media end-to-end: after reboot with Full WSSIZE, Go Profile 2 passed TLS 1.2/1.3 and signed media ranges on the primary WAN; after quick-profile applied strategy 30, the check again passed; WAN capture showed `win 1, wscale 2` on media SYN and ordinary window scale on control HTTPS; client playback remains unverified
- [x] Blockcheck2 Go CLI launch and SUMMARY creation on router (`google.com`; TLS 1.2/1.3 available without bypass; service restored; ncat unavailable, so port-check skipped)
- [ ] Keenetic Entware: S90, ndm hook, iptables
- [ ] Merlin: firewall-start hook
- [ ] MIPS/arm бинарник стартует, Web UI отвечает, locks пишутся атомарно
- [ ] Offline bundle на устройстве без GitHub
- [ ] Update path: published `latest.json` → release apply with keep-if-exists on a router
