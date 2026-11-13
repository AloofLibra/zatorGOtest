# ZatorGO

Управляющий слой для **zapret2** на Go: установка, стратегии обхода DPI, списки доменов, firewall-хуки и Web UI.

Ядро [bol-van/zapret](https://github.com/bol-van/zapret) / `nfqws2` **не переписывается** — скачивается и запускается как внешняя зависимость. Lua-модули zator исполняются внутри `nfqws2`.

Это полная переписка bash-проекта [AloofLibra/zator](https://github.com/AloofLibra/zator) с breaking changes:

- вместо интерактивного CLI-меню — **минимальный bootstrap + Web UI**;
- свой установщик zapret2 (**без** `install_easy.sh`);
- своя раскладка `/opt/zator` + `/opt/zapret2`.

Релизы и автообновления: **[Koviand/zatorGOtest](https://github.com/Koviand/zatorGOtest)**.

---

## Быстрый старт (установка на устройство)

На роутере / VPS (нужен root и доступ в интернет):

```sh
curl -fsSL https://github.com/Koviand/zatorGOtest/releases/latest/download/zator-launcher.sh -o zator-launcher.sh
chmod +x zator-launcher.sh
./zator-launcher.sh
```

Лаунчер:

1. определяет архитектуру CPU;
2. скачивает `zator-linux-<arch>` из latest release;
3. выполняет `zator install`;
4. поднимает Web UI на порту **17682**.

Откройте в браузере: `http://<IP-устройства>:17682/`

По умолчанию Web UI **без токена**. Для защиты задайте `"token"` в `/opt/zator/config/zator.json` и перезапустите службу / `zator webui`.

### Обновление

- из Web UI: **Настройки → Обновления → Проверить / Установить**;
- из CLI: `zator update`.

Источник обновлений: `Koviand/zatorGOtest`. Переопределение:

```sh
export ZATOR_OWNER=Koviand
export ZATOR_REPO=zatorGOtest
# или ZATOR_LATEST_URL=https://api.github.com/repos/.../releases/latest
```

### Переустановка «с нуля»

```sh
zator uninstall -y
curl -fsSL https://github.com/Koviand/zatorGOtest/releases/latest/download/zator-launcher.sh -o zator-launcher.sh
chmod +x zator-launcher.sh
./zator-launcher.sh
```

---

## CLI (bootstrap)

```text
zator install [-port 17682] [-source fork|official] [-zapret=true] [-skip-service]
zator update
zator uninstall -y
zator webui [-port 17682]
zator status
zator version
```

Обычная повседневная работа — через Web UI; CLI нужен для install/update/uninstall и аварийного запуска UI.

---

## Архитектуры

Статическая сборка (`CGO_ENABLED=0`):

`arm`, `arm64`, `mips`, `mips64`, `mipsel`, `mipsel64`, `ppc`, `x86`, `x86_64`

```sh
make cross    # → dist/zator-linux-*
make test
```

---

## Раскладка на устройстве

| Путь | Содержимое |
| --- | --- |
| `/opt/zator/bin/zator` | бинарник |
| `/opt/zator/webui` | статика Web UI |
| `/opt/zator/lua` | Lua-модули для nfqws2 |
| `/opt/zator/lists` / `extra_strats` | hostlist’ы |
| `/opt/zator/files/fake` | TLS/WG blob (`.bin`) |
| `/opt/zator/state/orchestra` | локи стратегий, overrides |
| `/opt/zator/config/zator.json` | настройки zator (порт, token, …) |
| `/opt/zapret2` | runtime zapret2 (`nfqws2`, `config`, …) |

---

## Структура репозитория

```text
cmd/zator/           # точка входа CLI
internal/
  app/               # install / update / bootstrap
  config/            # разбор и правка NFQWS2_OPT / config zapret2
  orchestra/         # локи стратегий, overrides (blob/mode/clone)
  webapi/            # HTTP API = CGI-совместимый слой для Web UI
  deploy/            # GitHub releases, CompareLatest, apply update
  sweep/             # supersweep / подбор стратегий
  netcheck/          # TLS 1.2/1.3 проверки
  service/           # start/stop/restart zapret2
  domains/           # списки доменов
  …
assets/              # config.default, fake blobs, lua, lists
webui-src/           # Vue 3 + Vite исходники панели
assets/webui/        # собранная статика (вшивается в бинарник)
docs/PARITY.md       # паритет с bash-zator
```

---

## Web UI — разделы

| Раздел | Путь | Назначение |
| --- | --- | --- |
| **Обзор** | `#/` | статус zapret2, сводка режимов, быстрый TLS-check, старт/стоп |
| **Стратегии** | `#/strategies` | локи профилей 1–10, проверка TLS 1.2/1.3 после лока, supersweep |
| **Вручную** | `#/manual` | тестовая лаборатория: добавить свою стратегию / загрузить blob |
| **Домены** | `#/domains` | hostlist / custom_rkn / Discord и др. |
| **Настройки** | `#/settings/…` | обновления, провайдер, бэкапы, режимы, TLS/WG, порты, scopes, сырой конфиг |

### Логика стратегий (кратко)

- Стратегии — строки `--lua-desync=…:strategy=N` в `NFQWS2_OPT` конфига zapret2.
- **Лок** пишет номер стратегии в `locked.tsv` (или `locked.manual.tsv` для fallback 8/9).
- `max_strategy` считается по конфигу (с учётом `--template` / `--import`).
- После «Зафиксировать» UI автоматически гоняет `check.cgi` и показывает детали **TLS 1.2 / TLS 1.3**.

### Вкладка «Вручную» (тест)

1. **Стратегия профиля** — вставляет новую desync-строку в блок профиля как `max+1`, опционально сразу лочит.
2. **Blob** — загружает `.bin` в `/opt/zator/files/fake` (`tls_*.bin` / `custom_tls.bin` или `wg_initial_fake_*.bin`), опционально сразу выбирает как текущий.

API:

```http
POST /cgi-bin/settings.cgi
  setting=strategy_register&profile=1&desync=…&lock=1

POST /cgi-bin/blob-upload.cgi?kind=tls&name=tls_my.bin&select=1
Content-Type: application/octet-stream
<raw bytes>
```

---

## Разработка

Требования: Go 1.22+, Node.js 20+ (для Web UI).

```sh
# тесты backend
go test ./...

# сборка бинарника
go build -o bin/zator ./cmd/zator

# Web UI
cd webui-src
npm ci
npm run build          # → assets/webui/
```

После изменения `webui-src` обязателен `npm run build`, иначе в релиз попадёт старая статика.

Кросс-сборка релизов: `make cross` (и CI на push тега `v*`).

---

## Конфигурация zator.json (пример)

```json
{
  "port": 17682,
  "token": "",
  "tracking": "latest"
}
```

- `token` пустой — открытый UI (удобно в LAN);
- непустой — нужен `Authorization: Bearer …`, Basic, `?token=` или cookie `zator_token`.

---

## Безопасность

- Не выставляйте порт 17682 в интернет без токена / firewall.
- `strategy_register` и `blob-upload` меняют рабочий конфиг zapret2 — используйте «Вручную» осознанно; перед экспериментами сделайте бэкап в UI.
- Обновления тянут бинарники с GitHub Releases — проверяйте, что `ZATOR_OWNER`/`ZATOR_REPO` указывают на доверенный репозиторий.

---

## Лицензия и происхождение

Проект продолжает идею bash-zator (AloofLibra) в Go-реализации с иным UX (Web UI first). Ядро обхода — zapret2 / nfqws2 (bol-van) и связанные Lua-стратегии.
