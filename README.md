# gpx2gif — генератор GIF-анимации по GPX-трекам

Утилита конвертирует один или несколько GPX-треков в анимированную GIF-карту.  
Поддерживает наложение треков на тайловые или статические карты (OpenTopoMap, ESRI Satellite, MapTiler Satellite, Stamen и др.) и рендер с синхронизацией по времени точек.

## Возможности

- Загрузка одного или нескольких GPX-треков (`-in`).
- Анимация треков во времени (по timestamp в точках GPX).
- Наложение на:
  - статическую картинку (`-staticURL`);
  - тайловые карты через `-tilesPreset` или `-tilesURL`.
- Кэширование тайлов (`-tileCache`) и ограничение RPS (`-tilesRPS`).
- Настройка размера кадра, FPS, длительности итогового GIF.
- Управление цветами и толщиной линий для каждого трека.
- Центровка bbox с отступами (`-margin`).
- Выбор способа подгонки карты под квадратный кадр (`-tileFit contain|cover`).

## Установка

```bash
git clone https://github.com//s0ultr4d3r/psstelebot.git
cd psstelebot
go build -o psstelebot

## Флаги запуска

| Флаг              | Описание                                                                 | Значение по умолчанию |
|-------------------|--------------------------------------------------------------------------|------------------------|
| `-in`             | Путь к GPX-файлу (можно указывать несколько раз)                        | `track.gpx`            |
| `-out`            | Куда сохранить GIF                                                      | `synced.gif`           |
| `-size`           | Размер кадра (квадрат, px)                                              | `512`                  |
| `-fps`            | Частота кадров (frames per second)                                      | `20`                   |
| `-duration`       | Длительность итогового GIF (например, `12s`)                            | `12s`                  |
| `-margin`         | Поля от краёв bbox (0..0.25)                                            | `0.05`                 |
| `-bg`             | Цвет фона, если нет карты (hex)                                         | `#000000`              |
| `-lineColors`     | Список цветов линий для треков, через запятую (hex)                     | `#ffffff,#ff3b30,#34c759,#007aff,#ffcc00,#af52de` |
| `-lineWidth`      | Толщина линии трека в пикселях                                          | `4`                    |
| `-tileFit`        | Подгонка карты: `contain` (с отступами) или `cover` (обрезка под квадрат)| `contain`              |
| `-staticURL`      | Шаблон URL статической карты с плейсхолдерами `{minLon},{minLat},...`   | —                      |
| `-tilesPreset`    | Предустановленные карты: `opentopomap`, `esri-satellite`, `maptiler-satellite`, `stamen-terrain-bg` | — |
| `-tilesURL`       | Пользовательский шаблон тайлов `{z}/{x}/{y}`                            | —                      |
| `-tileCache`      | Папка для кэша тайлов                                                   | `.tile-cache`          |
| `-tilesRPS`       | Tile requests per second (ограничение RPS)                              | `1.0`                  |
| `-tilesBurst`     | Размер burst для rate-limit                                             | `1`                    |
| `-tilesTimeout`   | Таймаут загрузки тайла                                                  | `8s`                   |
| `-timeout`        | Жёсткий таймаут всего процесса                                          | `10m`                  |
| `-pprof`          | Запуск pprof (например, `127.0.0.1:6060`), пусто = выключено            | —                      |



Примеры запуска
1. Один трек, топографическая карта OpenTopoMap
./gpx2gif \
  -in track.gpx \
  -out track_topo.gif \
  -size 1024 -fps 20 -duration 12s \
  -tilesPreset opentopomap \
  -tileCache ~/.cache/gpx2gif/tiles \
  -tilesRPS 1 -tilesBurst 1 \
  -tileFit cover \
  -lineColors "#ff3b30" \
  -lineWidth 6

2. Два трека: красный и синий, спутниковая карта ESRI
./gpx2gif \
  -in track1.gpx -in track2.gpx \
  -out tracks_satellite.gif \
  -size 1024 -fps 20 -duration 15s \
  -tilesPreset esri-satellite \
  -tileCache ~/.cache/gpx2gif/tiles \
  -tilesRPS 3 \
  -tileFit cover -margin 0.08 \
  -lineColors "#ff3b30,#007aff" \
  -lineWidth 8

3. Спутниковая карта MapTiler (требуется ключ)
export MAPTILER_KEY=pk_xxxxxxxxxxxxxxxxx
./gpx2gif \
  -in track.gpx \
  -out track_maptiler.gif \
  -size 1536 -fps 25 -duration 20s \
  -tilesPreset maptiler-satellite \
  -tileCache ~/.cache/gpx2gif/tiles \
  -tilesRPS 5 -tilesBurst 2 \
  -tileFit cover \
  -lineColors "#34c759" \
  -lineWidth 5


## Production Linux build

Production runs the bot and renderer side by side in `/root/psstelebot`.
The bot's development default is `./bin/psstelebot`, so the deployed build must
set the renderer path explicitly. Preserve this linker setting when rebuilding:

```sh
GOTOOLCHAIN=go1.27.3 CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -ldflags='-s -w' -o dist/psstelebot ./cmd/psstelebot
GOTOOLCHAIN=go1.27.3 CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -ldflags='-s -w -X main.rendererPath=/root/psstelebot/psstelebot' -o dist/psstelebot-bot ./cmd/psstelebot-bot
```

Build from a clean fixed commit and retain its SHA, Go version, dependency list,
build flags and artifact checksums. Back up both deployed binaries before replacing
them together. Validate the renderer with a synthetic GPX before restarting the bot;
never run a second polling process with the production token.
