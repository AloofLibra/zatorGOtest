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
| Интерактивное CLI-меню | Минимальный bootstrap + **Web UI** |
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

- Web UI → **Настройки → Обновления → Проверить / Установить**
- CLI: `zator update`

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

`CGO_ENABLED=0`: `arm`, `arm64`, `mips`, `mips64`, `mipsel`, `mipsel64`, `ppc`, `x86`, `x86_64`, `riscv64`  
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
| **Стратегии** | `#/strategies` | локи профилей 1–10, проверка после лока |
| **Вручную** | `#/manual` | desync / blob / googlevideo / суперсвип |
| **Домены** | `#/domains` | hostlist / custom_rkn / исключения |
| **Настройки** | `#/settings/…` | обновления, провайдер, бэкапы, TLS/WG, порты, scopes |

### Логика стратегий

- Стратегии — строки `--lua-desync=…:strategy=N` в `NFQWS2_OPT`.
- **Лок** пишет номер в `locked.tsv` (или `locked.manual.tsv` для fallback 8/9).
- После «Зафиксировать» UI гоняет проверку и показывает **TLS 1.2 / TLS 1.3**.

### Вкладка «Вручную»

1. **Стратегия** — вставляет desync в блок профиля как `max+1`, опционально лочит.
2. **Blob** — `.bin` в `/opt/zator/files/fake`, опционально сразу выбирает.

Подробный HTTP API: [`docs/API.md`](docs/API.md) (`/api/*`; `/cgi-bin/*.cgi` — алиас).

---

## CLI

```text
zator install [-port 17682] [-password …] [-no-auth] [-source fork|official] [-zapret=true] [-skip-service]
zator update
zator uninstall -y [--purge]
zator auth status|set-password|enable|disable
zator service status|warnings|start|stop|restart [unit]
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

Соберите bundle под архитектуру целевого роутера (например, `TARGET_ARCH=arm64 bash scripts/pack-offline.sh`). Bundle содержит соответствующий бинарник в `vendor/`; для старых версий он также вложен в `zator-full.tar.gz`. Запустите `sh zator-launcher.sh` из каталога bundle, чтобы проверить checksum и выполнить установку без GitHub. Для обновления уже установленной системы используйте `Z2R_OFFLINE=1 ZATOR_BUNDLE_DIR=/путь/к/bundle zator update`. Необходимые системные пакеты и модули ядра должны быть доступны локально или уже установлены; bundle их не упаковывает.

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
  config/            # NFQWS2_OPT / config zapret2
  orchestra/         # локи стратегий, overrides
  webapi/            # HTTP API для Web UI
  deploy/            # GitHub releases / apply update
  sweep/             # supersweep
  netcheck/          # TLS 1.2/1.3
  service/           # start/stop/restart
  domains/           # списки доменов
assets/              # config.default, fake, lua, lists, extra_strats, asn
webui-src/           # Vue 3 + Vite
assets/webui/        # собранная статика (вшивается в бинарник)
vendor/              # опционально: локальные архивы zapret2
docs/                # API.md, PARITY.md
```

Списки, стратегии, Lua и ASN живут в `assets/` и вшиваются в бинарник — **без чужих репозиториев на runtime**. Удалённые feed’ы выключены по умолчанию (`Z2R_GOOGLEVIDEO_IPS_URL`, `Z2R_RECS_URL`, …). Установка zapret2 сначала ищет архив в `vendor/` / `$ZATOR_VENDOR`.

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
