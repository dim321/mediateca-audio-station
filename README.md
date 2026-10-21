# mediateca-audio-station

Аудио-агент для Linux mini-PC. Это клиент уже работающего Broadcast Hub: процесс забирает суточный таймлайн, кэширует файлы и играет их локальным `mpv` на один псевдоэкран. Плейлисты агент не создаёт.

## Сборка

Нужен Go 1.22 или новее. CGo не используется.

```bash
CGO_ENABLED=0 go build -o mediateca-audio-station ./cmd/audio-station
```

Поставьте бинарь в `/usr/local/bin/mediateca-audio-station`.

## Хост

На mini-PC должны стоять `mpv` и клиент NTP (`chrony` или `systemd-timesyncd`). Часы в UTC. Агент не стартует, пока часы не скорректированы: в unit-файле `After=` и `Wants=` укажите тот NTP-юнит, который реально есть в образе (`chronyd.service`, `chrony.service` или `systemd-timesyncd.service`). В репозитории в `After=` записан `chronyd.service`.

## Пользователь и каталоги

```bash
sudo useradd --system --home /var/lib/mediateca-audio-station --shell /usr/sbin/nologin mediateca-audio
sudo install -d -o mediateca-audio -g mediateca-audio -m 0700 /var/lib/mediateca-audio-station
sudo install -d -o mediateca-audio -g mediateca-audio -m 0700 /etc/mediateca-audio-station
sudo install -o mediateca-audio -g mediateca-audio -m 0600 /dev/null /etc/mediateca-audio-station/config.yaml
```

`data_dir` принадлежит `mediateca-audio` и имеет режим `0700`. Файл конфигурации — режим `0600`: если в правах есть биты группы или остальных (`0077`), процесс завершается с ненулевым кодом и не крутится в crash-loop из-за тихого отказа. То же самое, если `mpv_path` не находится.

## Конфигурация

`screen_id` — идентификатор единственного псевдоэкрана этой аудио-станции в Hub.

```yaml
hub_base_url: "https://hub.example"
agent_token: "<token issued once by the hub>"
data_dir: "/var/lib/mediateca-audio-station"
screen_id: 42
late_threshold: "2s"
poll_interval: "1m"
mpv_path: "mpv"
# audio_device: "alsa/hdmi:CARD=PCH,DEV=0"
```

`late_threshold` и `poll_interval` — длительности Go (`time.ParseDuration`). Пустые значения означают `2s` и `1m`. Пустой `mpv_path` означает `mpv`. `audio_device` необязателен: пустая строка не попадает в аргументы mpv.

## systemd

```bash
sudo cp packaging/mediateca-audio-station.service /etc/systemd/system/
sudo systemctl daemon-reload
sudo systemctl enable --now mediateca-audio-station
```

Юнит запускается от `mediateca-audio`, `Restart=always`, `RestartSec=2`, `NoNewPrivileges=true`.

Журнал:

```bash
journalctl -u mediateca-audio-station -f
```

Что искать:

- `clip skip` с `reason=late` — клип не стартовал, потому что момент `starts_at` плюс `late_threshold` уже прошёл. Перезапуск процесса в середине клипа тоже попадает сюда: доигрывание с середины нет.
- `clip skip` с `reason=missing_file` — файла нет в кэше, и окно опоздания уже закрыто. Пока окно открыто, агент ждёт файл и ничего не пропускает.
- `clip skip` с `reason=conflict` — на тот же `starts_at` есть ролик с меньшим `position`.
- `ignored_entry` — запись пакета без времени, длительности, URL или MIME.
- `hub token rejected` — Hub ответил 401. Сообщение пишется один раз, пока следующий опрос config или package не вернёт 200 или 304. Очередь play events в это время не отправляется, уже скачанный таймлайн продолжает играть.
- `mpv exit before the scheduled stop` — `mpv` завершился сам, до плановой остановки по концу слота.
- `hub screen list does not match configured screen_id` — в сохранённом конфиге Hub нет настроенного `screen_id`, экранов ноль, или их больше одного. Играет только настроенный id, и только если он есть в списке. Пока `config.json` ещё ни разу не сохранялся, играет локальный `screen_id`.

Токен и заголовок `Authorization` в журнал не пишутся.

## Проверка

Тесты не требуют установленного `mpv`:

```bash
go test ./...
```
