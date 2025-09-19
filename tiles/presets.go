package tiles

import (
	"fmt"
	"net/url"
	"strings"
)

type Preset struct {
	Name        string
	URLTmpl     string // .../{z}/{x}/{y}.png
	Attribution string
	MinZoom     int
	MaxZoom     int
	Headers     map[string]string // optional
}

var Presets = map[string]Preset{
	"opentopomap": {
		Name:        "OpenTopoMap",
		URLTmpl:     "https://tile.opentopomap.org/{z}/{x}/{y}.png",
		Attribution: "© OpenTopoMap (CC-BY-SA), © OpenStreetMap contributors",
		MinZoom:     0, MaxZoom: 17,
	},
	"esri-satellite": {
		Name:        "ESRI World Imagery",
		URLTmpl:     "https://server.arcgisonline.com/ArcGIS/rest/services/World_Imagery/MapServer/tile/{z}/{y}/{x}",
		Attribution: "© Esri, Maxar, Earthstar Geographics",
		MinZoom:     0, MaxZoom: 20,
	},
	"maptiler-satellite": {
		Name:        "MapTiler Satellite",
		URLTmpl:     "https://api.maptiler.com/tiles/satellite/{z}/{x}/{y}.jpg?key=${MAPTILER_KEY}",
		Attribution: "© MapTiler, © OpenStreetMap contributors, © NASA",
		MinZoom:     0, MaxZoom: 20,
	},
	"stamen-terrain-bg": {
		Name:        "Stadia Stamen Terrain BG",
		URLTmpl:     "https://tiles.stadiamaps.com/tiles/stamen_terrain_background/{z}/{x}/{y}.png?api_key=${STADIA_KEY}",
		Attribution: "© Stadia Maps, © Stamen Design, © OpenStreetMap contributors",
		MinZoom:     0, MaxZoom: 18,
	},
}

func (p Preset) FillURL(z, x, y int) (string, error) {
	u := strings.ReplaceAll(p.URLTmpl, "{z}", fmt.Sprintf("%d", z))
	u = strings.ReplaceAll(u, "{x}", fmt.Sprintf("%d", x))
	u = strings.ReplaceAll(u, "{y}", fmt.Sprintf("%d", y))
	_, err := url.Parse(u)
	return u, err
}
