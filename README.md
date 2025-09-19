# psstelebot

export STADIA_KEY="...твой ключ..."

go run . \
  -in LEShA-05-31_154519.gpx \
  -in LORI-05-31_164517.gpx \
  -out synced.gif \
  -size 512 -fps 20 -duration 12s -timeout 10m \
  -tilesURL "https://tiles.stadiamaps.com/tiles/alidade_smooth/{z}/{x}/{y}.png?api_key=${STADIA_KEY}" \
  -lineColors "#ff3b30,#34c759,#007aff,#ffcc00"
