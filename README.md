<p align="center">
  <img src=".github/banner.png" alt="ZatorGO — операторская панель для zapret2" width="100%">
</p>

<p align="center">
  <strong>ZatorGO</strong> — управляющий слой для <strong>zapret2</strong> на Go:<br>
  установка, стратегии обхода DPI, hostlist’ы, firewall-хуки и Web UI-консоль.
</p>

<p align="center">
  <a href="https://github.com/Koviand/zatorGO/releases/latest"><img src="https://img.shields.io/github/v/release/Koviand/zatorGO?style=flat-square&label=release&color=1d7a8c" alt="Latest release"></a>
  <a href="https://github.com/Koviand/zatorGO/actions/workflows/ci.yml"><img src="https://img.shields.io/github/actions/workflow/status/Koviand/zatorGO/ci.yml?branch=main&style=flat-square&label=CI" alt="CI"></a>
  <a href="https://github.com/Koviand/zatorGO/actions/workflows/release.yml"><img src="https://img.shields.io/github/actions/workflow/status/Koviand/zatorGO/release.yml?branch=main&style=flat-square&label=release%20build" alt="Release build"></a>
  <a href="LICENSE"><img src="https://img.shields.io/github/license/Koviand/zatorGO?style=flat-square&color=0f172a" alt="License GPL-3.0"></a>
  <a href="https://github.com/Koviand/zatorGO/releases"><img src="https://img.shields.io/github/downloads/Koviand/zatorGO/total?style=flat-square&color=1f7a4d" alt="Downloads"></a>
</p>

<p align="center">
  <a href="#быстрый-старт">Установка</a> ·
  <a href="#возможности">Возможности</a> ·
  <a href="#web-ui">Web UI</a> ·
  <a href="#cli">CLI</a> ·
  <a href="docs/API.md">API</a> ·
  <a href="https://github.com/Koviand/zatorGO/releases">Релизы</a>
</p>

---

## Зачем это

Ядро [bol-van/zapret](https://github.com/bol-van/zapret) / `nfqws2` **не переписывается** — ставится и запускается как зависимость. ZatorGO даёт операторский контур вокруг него: bootstrap, локи стратегий, списки, health и панель управления.

Полная переписка bash-проекта [AloofLibra/zator](https://github.com/AloofLibra/zator) с другим UX:

| Было (bash-zator) | Стало (ZatorGO) |
| --- | --- |
| Интерактивное CLI-меню | Go CLI-меню + **Web UI** |
| `install_easy.sh` | Свой установщик zapret2 |
| Своя раскладка путей | `/opt/zator` + `/opt/zapret2` |

Релизы и автообновления: **[Koviand/zatorGO](https://github.com/Koviand/zatorGO)**.

---

## Быстрый старт

На роутере / VPS (нужны **root** и интернет):

```sh
curl -fsSL https://github.com/Koviand/zatorGO/releases/latest/download/zator-launcher.sh -o zator-launcher.sh
chmod +x zator-launcher.sh
./zator-launcher.sh
```

Лаунчер:

1. определяет архитектуру CPU;
2. скачивает `zator-linux-<arch>` из [latest release](https://github.com/Koviand/zatorGO/releases/latest);
3. выполняет `zator install` (запросит пароль Web UI);
4. поднимает панель на порту **17682**.

Откройте `http://<IP-устройства>:17682/` и войдите с паролем установки.

> Проверка доступа **включена по умолчанию**. Отключить только осознанно: `zator install -no-auth` или `zator auth disable -y`.

### Обновление

- Web UI → **Настройки → Обновления** → выберите режим и нажмите «Обновить»
- CLI: `zator update -mode app|config|full` (по умолчанию `app`, только Zator)

`app` (режим по умолчанию) обновляет бинарник Zator и WebUI/служебные ресурсы; `config` отдельно обновляет Lua, стратегии, списки, blobs и Blockcheck2 без замены бинарника; `full` явно применяет оба архива. Если под откат не хватает места в `/tmp`, интерфейс предупредит и предложит продолжить без резервной копии; CLI требует явный флаг `-continue-without-backup`.

```sh
export ZATOR_OWNER=Koviand
export ZATOR_REPO=zatorGO
# или ZATOR_LATEST_URL=https://api.github.com/repos/Koviand/zatorGO/releases/latest
```

### Переустановка с нуля

```sh
zator uninstall -y --purge
curl -fsSL https://github.com/Koviand/zatorGO/releases/latest/download/zator-launcher.sh -o zator-launcher.sh
chmod +x zator-launcher.sh
./zator-launcher.sh
```

`uninstall -y` снимает службы и runtime, но **сохраняет** config/state/lists. Полный снос — только `--purge`.

---

## Возможности

- **Операторская Web UI** — статус health, старт/стоп, локи стратегий, домены, настройки
- **Keenetic / Entware ready** — SysV-скрипты, nfqws- и webui-watchdog, firewall ensure
- **Self-contained** — списки, стратегии, Lua и ASN вшиты в бинарник; сеть — запасной путь
- **Безопасные обновления** — pre-update snapshot, post-update verify, откат бинарника
- **Supersweep** — подбор рабочих стратегий с TLS-проверками 1.2 / 1.3
- **Мультиархитектура** — статическая сборка под роутеры и VPS

---

## Архитектуры

`CGO_ENABLED=0`: `arm`, `arm64`, `mips`, `mipsel`, `x86`, `x86_64`, `riscv64`
(на Lexra/RLX лаунчер берёт `mipsel`)

```sh
make cross    # → dist/zator-linux-*
make test
```

---

## Web UI

| Раздел | Путь | Назначение |
| --- | --- | --- |
| **Обзор** | `#/` | health / warnings / TLS-check / старт·стоп·рестарт |
| **Стратегии** | `#/strategies` | локи активных профилей 1–10 (включая QUIC/5 + `h3-curl`), проверка после лока |
| **Подбор** | `#/sweep` | Supersweep и быстрый подбор (включая QUIC/5 в live-режиме) |
| **Свои стратегии** | `#/custom-strats` | конструктор, импорт blockcheck2/nfqws2, перенумерация и ремап локов |
| **Блобы и SNI** | `#/blobs-sni` | карта профилей, TLS/QUIC блобы, SNI-пул (rr/rand), свои бинарные блобы |
| **Домены** | `#/domains` | hostlist / custom_rkn / исключения |
| **Настройки** | `#/settings/…` | обновления, провайдер, бэкапы, WG, WSSIZE, порты, scopes, диагностика |

### Логика стратегий

- Стратегии — строки `--lua-desync=…:strategy=N` в `NFQWS2_OPT`.
- **Лок** пишет номер в `locked.tsv` (или `locked.manual.tsv` для fallback 8/9).
- После «Лок» UI гоняет проверку: TLS 1.2 / TLS 1.3 (+ media-диапазоны для П2 и HTTP/3 `h3-curl` для П5).

### «Свои стратегии» и «Блобы и SNI»

1. **Свои стратегии** — добавление и импорт (`blockcheck2`/`nfqws2`, отклонение `zapret1`) в блоки `tpl`/`p4`/`p5`/`p6`/`p7`, автоматическое назначение номеров и перенос активных локов при применении и обновлении конфига.
2. **Блобы и SNI** — загрузка бинарных фейков (до 256 КиБ), глобальный и по-профильный выбор TLS/QUIC блобов (`z2r_prof_*` с сохранением в `blob_files.tsv`), режим `classic/clone`, SNI-пины и ротация наборов `sni_zator.txt`/`sni_user.txt`.

Подробный HTTP API: [`docs/API.md`](docs/API.md) (`/api/*`; `/cgi-bin/*.cgi` — алиас).

---

## CLI

В SSH-терминале `zator` без аргументов открывает лёгкое цифровое меню; для явного запуска используйте `zator menu`. В скриптах и без TTY команда без аргументов по-прежнему печатает краткий статус и не ждёт ввода. Меню работает обычным последовательным текстом, поддерживает `NO_COLOR` и `TERM=dumb` и не требует запуска Web UI.

Из меню доступны статус и диагностика, просмотр/фиксация/сброс/проверка стратегий, полный и быстрый Supersweep с прогрессом и отдельной отменой, списки доменов (включая импорт файла), основные переключатели обхода, службы, обновления, резервные копии, авторизация и Blockcheck2. Эти действия вызывают существующую Go-логику ZatorGO напрямую.

Supersweep работает, пока открыт процесс `zator`. `Ctrl+C` останавливает просмотр прогресса, но не отменяет тестирование; для отмены выберите отдельный пункт. При попытке выйти из меню во время активного прогона CLI предупредит, что выход завершит прогон, и запросит подтверждение отмены.

```text
zator                 # меню в терминале; краткий статус без TTY
zator menu            # явный запуск меню (требует TTY)
zator install [-port 17682] [-password …] [-no-auth] [-source fork|official] [-zapret=true] [-skip-service]
zator update
zator uninstall -y [--purge]
zator auth status|set-password|enable|disable
zator service status|warnings|start|stop|restart [unit]
zator custom list|add|edit|enable|disable|delete|import|apply
zator blobs list|upload|add|edit|delete|apply
zator sni status|pin|pool|policy|set-write|set-delete
zator h3-curl status|install|update|remove
zator locks clear -y
zator restore z2r_backup_YYYYMMDD_HHMMSS.tar
zator rollback
zator blockcheck2 -y <domain>
zator webui [-port 17682]
zator status
zator errors
zator version
```

CLI также позволяет проверить/перезапустить службы, посмотреть предупреждения служб и журнал
ошибок nfqws2, сбросить фиксации стратегий с предварительным backup, восстановить настройки и запустить Blockcheck2.
Blockcheck2 временно останавливает zapret2, поэтому команда требует `-y` и автоматически запускает службу после проверки.

### Установка и обновление без GitHub

Соберите bundle под архитектуру целевого роутера (например, `TARGET_ARCH=arm64 bash scripts/pack-offline.sh`). Bundle содержит отдельные `zator-app.tar.gz` и `zator-config.tar.gz`, а также бинарники и checksums в `vendor/`. Запустите `sh zator-launcher.sh` из каталога bundle, чтобы выполнить установку без GitHub. Для обновления используйте `Z2R_OFFLINE=1 ZATOR_BUNDLE_DIR=/путь/к/bundle zator update -mode full`. Необходимые системные пакеты и модули ядра должны быть доступны локально или уже установлены; bundle их не упаковывает.

Release-бинарники собираются Garble `-tiny` и упаковываются UPX `--best --lzma`. Официально поддерживаются семь архитектур из списка выше; MIPS64, MIPSel64 и PPC64 исключены из release-сборок. Для такой же упаковки локального offline-bundle установите Garble v0.18.0 и UPX v5.3.0 и запустите `ZATOR_GARBLE=1 ZATOR_UPX=1 TARGET_ARCH=arm64 bash scripts/pack-offline.sh`. Без этих переменных локальная кросс-сборка остаётся обычной.

---

## Раскладка на устройстве

| Путь | Содержимое |
| --- | --- |
| `/opt/zator/bin/zator` | бинарник |
| `/opt/zator/webui` | статика Web UI |
| `/opt/zator/lua` | Lua-модули для nfqws2 |
| `/opt/zator/lists` / `extra_strats` | hostlist’ы и стратегии |
| `/opt/zator/files/fake` | TLS/WG blob (`.bin`) |
| `/opt/zator/state/orchestra` | локи и overrides |
| `/opt/zator/config/zator.json` | порт, auth, password_hash, … |
| `/opt/zapret2` | runtime zapret2 (`nfqws2`, `config`, …) |

---

## Структура репозитория

```text
cmd/zator/           # CLI
internal/
  app/               # install / update / bootstrap
  config/            # NFQWS2_OPT / config zapret2 / markers
  orchestra/         # локи стратегий, overrides, blob_files, sni_pool, remap
  customblobs/       # пользовательские бинарные блобы и секция #Z2R_CUSTOM_BLOBS
  customstrats/      # пользовательские стратегии, валидатор и импорт blockcheck2
  orchestra/         # локи стратегий, overrides
  webapi/            # HTTP API для Web UI
  deploy/            # GitHub releases / apply update
  sweep/             # supersweep
  netcheck/          # TLS 1.2/1.3
  service/           # start/stop/restart
  domains/           # списки доменов
assets/              # исходники ресурсов для app/config архивов
webui-src/           # Vue 3 + Vite
assets/webui/        # собранная статика (в zator-app.tar.gz)
vendor/              # опционально: локальные архивы zapret2
docs/                # API.md, PARITY.md
```

Списки, стратегии, Lua, ASN и WebUI живут в `assets/` и поставляются двумя архивами отдельно от бинарника. На роутере загрузки, staging и rollback snapshots хранятся в `/tmp`; неизменившиеся файлы не переписываются, а изменённые пользователем списки сохраняются. Удалённые feed’ы выключены по умолчанию (`Z2R_GOOGLEVIDEO_IPS_URL`, `Z2R_RECS_URL`, …). Установка zapret2 сначала ищет архив в `vendor/` / `$ZATOR_VENDOR`.

Паритет с bash-zator: [`docs/PARITY.md`](docs/PARITY.md).

---

## Разработка

Требования: **Go 1.22+**, **Node.js 20+** (Web UI).

```sh
go test ./...
go build -o bin/zator ./cmd/zator

cd webui-src
npm ci
npm run build          # → assets/webui/
```

После правок в `webui-src` обязателен `npm run build`, иначе в релиз попадёт старая статика.  
Кросс-сборка: `make cross` (и CI на push тега `v*`).

---

## Конфигурация `zator.json`

```json
{
  "port": 17682,
  "auth_enabled": true,
  "password_hash": "$2a$10$…",
  "session_token": "…",
  "zapret_source": "fork"
}
```

- `auth_enabled: false` — открытый UI (только после `-no-auth` / `zator auth disable`);
- `auth_enabled: true` — пароль + cookie `zator_session` / Basic / Bearer;
- `zator update` **не** снимает пароль и auth.

CLI: `zator auth set-password`, `auth enable`, `auth disable -y`, `auth status`.

---

## Безопасность

- Порт **17682** слушает все интерфейсы — на VPS обязателен пароль (или firewall / reverse proxy).
- Не используйте `-no-auth` на машинах из интернета.
- «Вручную» меняет рабочий конфиг zapret2 — перед экспериментами сделайте бэкап в UI.
- Обновления с GitHub Releases — держите `ZATOR_OWNER` / `ZATOR_REPO` на доверенном репозитории.

Сообщения об уязвимостях: см. [SECURITY.md](.github/SECURITY.md).

---

## Лицензия и происхождение

[GPL-3.0](LICENSE). Идея bash-zator (AloofLibra) в Go-реализации с UX «Web UI first».  
Ядро обхода — zapret2 / nfqws2 ([bol-van/zapret](https://github.com/bol-van/zapret)) и связанные Lua-стратегии.
