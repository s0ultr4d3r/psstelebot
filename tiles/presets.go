package tiles

import "strings"

// Preset описывает источник тайлов.
type Preset struct {
	Name        string
	URLTmpl     string            // .../{z}/{x}/{y}.png (или jpg); допускается порядок {z}/{y}/{x}
	Attribution string
	MinZoom     int
	MaxZoom     int
	Headers     map[string]string // опционально: дополнительные HTTP-заголовки
}

// FillURL — заполнение плейсхолдеров.
// ВОЗВРАЩАЕТ (string, error), как ожидает fetcher.go.
func (p Preset) FillURL(z, x, y int) (string, error) {
	return Sub(p.URLTmpl, z, x, y), nil
}

// Sub — универсальная подстановка {z},{x},{y}.
func Sub(tmpl string, z, x, y int) string {
	s := tmpl
	s = strings.ReplaceAll(s, "{z}", itoa(z))
	s = strings.ReplaceAll(s, "{x}", itoa(x))
	s = strings.ReplaceAll(s, "{y}", itoa(y))
	return s
}

func itoa(i int) string {
	const digits = "0123456789"
	if i == 0 {
		return "0"
	}
	n := i
	neg := false
	if n < 0 {
		neg = true
		n = -n
	}
	var b [20]byte
	pos := len(b)
	for n > 0 {
		pos--
		b[pos] = digits[n%10]
		n /= 10
	}
	if neg {
		pos--
		b[pos] = '-'
	}
	return string(b[pos:])
}

// Готовые пресеты. Можно дополнять.
var Presets = map[string]Preset{
	"opentopomap": {
		Name:        "opentopomap",
		URLTmpl:     "https://a.tile.opentopomap.org/{z}/{x}/{y}.png",
		Attribution: "© OpenTopoMap (CC-BY-SA) — Data © OpenStreetMap contributors (ODbL)",
		MinZoom:     0, MaxZoom: 17,
	},
	"stamen-terrain-bg": {
		Name:        "stamen-terrain-bg",
		URLTmpl:     "https://tile.stamen.com/terrain-background/{z}/{x}/{y}.jpg",
		Attribution: "Map tiles by Stamen Design (CC BY 3.0) — Data © OpenStreetMap contributors (ODbL)",
		MinZoom:     0, MaxZoom: 18,
	},
	"esri-satellite": {
		Name:        "esri-satellite",
		URLTmpl:     "https://services.arcgisonline.com/ArcGIS/rest/services/World_Imagery/MapServer/tile/{z}/{y}/{x}",
		Attribution: "Source: Esri — Esri, Maxar, Earthstar Geographics, and the GIS User Community",
		MinZoom:     0, MaxZoom: 19,
		Headers:     map[string]string{"User-Agent": "psstelebot/gpx2gif"},
	},
	"maptiler-satellite": {
		Name:        "maptiler-satellite",
		URLTmpl:     "https://api.maptiler.com/tiles/satellite/{z}/{x}/{y}.jpg?key=YOUR_KEY",
		Attribution: "© MapTiler © OpenStreetMap contributors",
		MinZoom:     0, MaxZoom: 20,
	},
	"osm": {
		Name:        "osm",
		URLTmpl:     "https://tile.openstreetmap.org/{z}/{x}/{y}.png",
		Attribution: "© OpenStreetMap contributors (ODbL)",
		MinZoom:     0, MaxZoom: 19,
	},
}

// URLFromPreset: быстрый маппер “имя пресета → URL” с оверрайдом.
func URLFromPreset(name, override string) string {
	if override != "" {
		return override
	}
	if p, ok := Presets[name]; ok {
		return p.URLTmpl
	}
	return Presets["osm"].URLTmpl
}
