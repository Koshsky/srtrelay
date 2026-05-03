# srtrelay

`srtrelay` — утилита, которая принимает один входящий SRT-поток и раздаёт его нескольким исходящим SRT-слушателям. Все конечные точки работают в режиме слушателя (listener mode).


## Запуск

Пример: один публикатор на `:6200`, 4 слушателя на `:6201`, `:6202`, `:6203` и `:6204`

```bash
go run ./cmd/srtrelay \
  -input-addr :6200 \
  -output :6201 \
  -output :6202 \
  -output :6203 \
  -output :6204
```

Формат параметра output:

```text
-output addr[,streamid[,passphrase]]
```

Если `streamid` не указан — слушатель принимает любой stream id.
Если `passphrase` не указан — шифрование для этой точки отключено.

По умолчанию `-write-timeout` равен `0` (отключён), что обычно безопаснее для OBS-подписчиков, которые могут ставить на паузу или переоткрывать источники.


## Запуск через Docker

### Сборка и запуск

```bash
docker compose up -d --build
```

Флаг `-d` запускает контейнер в фоновом режиме. При первом запуске образ соберётся автоматически.

### Настройка параметров

Все параметры ретранслятора задаются в секции `command:` файла `docker-compose.yaml`:

```yaml
command:
  - "-input-addr=:6200"
  - "-output=:6201"
  - "-output=:6202"
  - "-output=:6203"
  - "-output=:6205"
```

Не забудь добавить соответствующий порт в секцию `ports:` для каждого нового выхода.

Доступные параметры:

| Параметр | Описание |
|---|---|
| `-input-addr` | Адрес входного SRT-слушателя |
| `-input-streamid` | Ожидаемый stream id от публикатора |
| `-input-passphrase` | Пароль для входящего подключения |
| `-output` | Выходной адрес в формате `addr[,streamid[,passphrase]]` |
| `-write-timeout` | Таймаут записи на выход (0 — отключён) |
| `-buffer-size` | Размер буфера чтения в байтах |

### Перезапуск после изменения параметров

После любых изменений в `docker-compose.yaml`:

```bash
docker compose down && docker compose up -d
```

### Просмотр логов

```bash
docker compose logs -f
```

## Тестирование с ffmpeg и ffplay

Запустите ретранслятор:

```bash
go run ./cmd/srtrelay \
  -input-addr :6200 \
  -output :6201 \
  -output :6202 \
  -output :6203 \
  -output :6204
# Опционально: явный режим без дедлайна для сценариев с OBS(?)
# -write-timeout 0
```

Опубликуйте SRT-поток в ретранслятор:

Файлы .mp4
```bash
ffmpeg -re -stream_loop -1 -i sample.mp4 \
  -c:v libx264 -preset veryfast -tune zerolatency \
  -c:a aac \
  -f mpegts \
  "srt://127.0.0.1:6200?mode=caller"
```

Файлы .mov
```bash
ffmpeg -re -stream_loop -1 -i sample.mov \
  -c:v libx264 -preset veryfast -tune zerolatency \
  -c:a aac \
  -f mpegts \
  "srt://127.0.0.1:6200?mode=caller"
```

Камера (Linux)
```bash
ffmpeg -f v4l2 -i /dev/video0 \
  -pix_fmt yuv420p -c:v libx264 -preset ultrafast -tune zerolatency \
  -f mpegts "srt://127.0.0.1:6200?mode=caller"
```


Подключите двух независимых зрителей к разным выходам:

```bash
ffplay "srt://127.0.0.1:6201?mode=caller"
```

```bash
ffplay "srt://127.0.0.1:6204?mode=caller"
```

Также можно использовать `ffmpeg` вместо `ffplay` в качестве приёмника для проверки:

```bash
ffmpeg -i "srt://127.0.0.1:6203?mode=caller" -f null -
```