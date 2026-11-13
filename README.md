# zator (Go)

Управляющий слой для **zapret2**: установка, стратегии, списки, firewall-хуки и Web UI.
Ядро bol-van/`nfqws2` не переписывается — скачивается как внешняя зависимость.

Это полная переписка bash-проекта [AloofLibra/zator](https://github.com/AloofLibra/zator) на Go с breaking changes:

- вместо интерактивного CLI-меню — **минимальный bootstrap + Web UI**;
- свой установщик zapret2 (**без** `install_easy.sh`);
- свой лаунчер и раскладка `/opt/zator` + `/opt/zapret2`.

## Быстрый старт

```sh
# на устройстве (роутер/VPS)
curl -fsSL https://github.com/Koviand/zatorGOtest/releases/latest/download/zator-launcher.sh -o zator-launcher.sh
chmod +x zator-launcher.sh
./zator-launcher.sh
```

Лаунчер определит архитектуру, скачает `zator-linux-<arch>`, выполнит `zator install` и поднимет Web UI (порт **17682**). По умолчанию Web UI открыт без токена; для защиты задайте `"token"` в `/opt/zator/config/zator.json`.

Обновления ZatorGO (Web UI «Проверить» / `zator update`) берутся из **Koviand/zatorGOtest**. Переопределение: `ZATOR_OWNER`, `ZATOR_REPO`, `ZATOR_LATEST_URL`.

### CLI (только bootstrap)

```text
zator install [-port 17682] [-source fork|official] [-zapret=true] [-skip-service]
zator update
zator uninstall -y
zator webui [-port 17682]
zator status
zator version
```

## Архитектуры

Собирается статически (`CGO_ENABLED=0`) под:

`arm`, `arm64`, `mips`, `mips64`, `mipsel`, `mipsel64`, `ppc`, `x86`, `x86_64`

```sh
make cross    # → dist/zator-linux-*
make test
```

## Раскладка

| Путь | Содержимое |
| --- | --- |
| `/opt/zator/bin/zator` | бинарник |
| `/opt/zator/{webui,lua,lists,fake,data,state,config}` | ресурсы и состояние |
| `/opt/zapret2` | runtime zapret2 (`nfqws2`, config, …) |

Lua-модули zator по-прежнему исполняются **внутри** `nfqws2`.

## Разработка

```sh
go test ./...
go build -o bin/zator ./cmd/zator
```

Паритет функций: [docs/PARITY.md](docs/PARITY.md).
