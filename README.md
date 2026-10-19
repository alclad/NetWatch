# Netwatch — мониторинг компьютеров в сети через ping

Однобинарное приложение на Go: периодически проверяет устройства ICMP ping'ом
и показывает статус в веб-интерфейсе. Имя устройства — ссылка:
**зелёная** — устройство в сети, **красная** — не отвечает, **серая** — ещё не проверено.

## Возможности

- Один исполняемый файл: HTML/CSS/JS встроены через `go:embed`.
- ICMP Echo Request из кода на Go (без внешней команды `ping`), IPv4 и IPv6.
- Обновление статусов в реальном времени без перезагрузки страницы (SSE).
- Добавление/редактирование/удаление устройств и настройка интервалов через UI.
- JSON-конфигурация сохраняется атомарно и переживает перезапуск.
- Параллельные проверки с ограничением concurrency, порог неудач,
  измерение задержки, время последней проверки и последнего успешного ответа.
- Поиск и фильтры по статусу, адаптивная вёрстка (ПК и телефон).
- Группы устройств: группировка списка с заголовками, фильтр по группе,
  поле группы при добавлении/редактировании (с автоподсказкой существующих).

## Сборка (Linux, статический бинарник)

```bash
# amd64
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -ldflags="-s -w" -o netwatch ./cmd/netwatch

# arm64
CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -trimpath -ldflags="-s -w" -o netwatch-arm64 ./cmd/netwatch
```

## Запуск

```bash
./netwatch
# откройте http://127.0.0.1:8080
```

Флаги:

| Флаг | По умолчанию | Описание |
|---|---|---|
| `-listen` | `127.0.0.1:8080` | Адрес HTTP-сервера. `0.0.0.0:8080` — доступ из сети (только за reverse proxy с авторизацией!) |
| `-config` | `netwatch.json` рядом с бинарником | Путь к JSON-конфигурации; пустая строка — без сохранения |
| `-raw-ping` | `false` | Raw ICMP-сокеты вместо непривилегированных (нужен `CAP_NET_RAW`) |

## Права на ICMP в Linux

По умолчанию используются **непривилегированные** datagram ICMP-сокеты
(`"udp"` в терминах x/net/icmp). Чтобы они работали для обычного пользователя,
разрешите его группе ping:

```bash
# Разово, до перезагрузки (для всех групп):
sudo sysctl net.ipv4.ping_group_range="0 2147483647"

# Постоянно:
echo 'net.ipv4.ping_group_range = 0 2147483647' | sudo tee /etc/sysctl.d/90-ping.conf
sudo sysctl --system
```

Альтернатива — выдать бинарнику capability (запуск с `-raw-ping`):

```bash
sudo setcap cap_net_raw+ep ./netwatch
./netwatch -raw-ping
```

Запуск всего приложения от root **не требуется и не рекомендуется**.
Если прав не хватает, интерфейс покажет понятную системную ошибку,
а не «все устройства недоступны».

## systemd (запуск от отдельного пользователя)

```bash
sudo useradd --system --home /var/lib/netwatch --create-home netwatch
sudo install -m755 netwatch /usr/local/bin/netwatch
# непривилегированный ping: см. ping_group_range выше
sudo install -m644 docs/netwatch.service /etc/systemd/system/
sudo systemctl daemon-reload
sudo systemctl enable --now netwatch
```

См. [docs/netwatch.service](docs/netwatch.service). Конфиг будет в
`/var/lib/netwatch/netwatch.json`.

## Пример конфигурации (`netwatch.json`)

Создаётся автоматически при первом сохранении; редактировать можно вручную
при остановленном приложении:

```json
{
  "devices": [
    {
      "id": "9f3ab2c1d4e5f60718293a4b",
      "name": "Сервер NAS",
      "address": "192.168.1.10",
      "url": "https://nas.local",
      "group": "Серверы"
    },
    {
      "id": "1b2c3d4e5f60718293a4b5c6",
      "name": "Роутер",
      "address": "192.168.1.1",
      "url": "",
      "group": "Сеть"
    }
  ],
  "settings": {
    "interval_seconds": 5,
    "timeout_seconds": 2,
    "failure_threshold": 3,
    "concurrency": 16
  }
}
```

- `url` необязателен; если задан — имя устройства становится ссылкой (только http/https).
- `group` необязателен (до 60 символов); устройства с одинаковой группой
  выводятся вместе под общим заголовком, по группе можно фильтровать список.
- Устройство становится «красным» после `failure_threshold` подряд неудач,
  «зелёным» — после первого же успешного ответа.
- Результаты проверок (статус, задержка) эфемерны и на диск не пишутся.

## Важно про интерпретацию статуса

Отсутствие ответа на ICMP **не означает**, что компьютер выключен:
firewall может блокировать ping, а ICMP может приоритизироваться/отбрасываться
сетью. Красный статус читайте как «не отвечает на ping», а не «выключен».

## Безопасность

В интерфейсе **нет авторизации**. По умолчанию сервер слушает только
`127.0.0.1`. Если нужен доступ из сети — поставьте приложение за reverse proxy
(nginx/Caddy) с авторизацией:

```nginx
location / {
    auth_basic "netwatch";
    auth_basic_user_file /etc/nginx/.htpasswd;
    proxy_pass http://127.0.0.1:8080;
    proxy_set_header Host $host;
    # для SSE:
    proxy_http_version 1.1;
    proxy_set_header Connection "";
    proxy_buffering off;
}
```

## Проверки качества

```bash
go vet ./...
go test ./...
go test -race ./...
```

Тесты не требуют сети и специальных прав: ICMP-клиент подменяется заглушкой.

## Структура проекта

```
cmd/netwatch/main.go      точка входа, флаги, сигналы, HTTP-сервер
internal/monitor/         ядро: состояние, планировщик, валидация, атомарное сохранение
internal/ping/            ICMP-клиент (datagram/raw сокеты)
internal/httpserver/      REST API, SSE, отдача встроенного UI
web/                      go:embed + статика (index.html, app.js, style.css)
docs/netwatch.service     пример systemd unit
```
