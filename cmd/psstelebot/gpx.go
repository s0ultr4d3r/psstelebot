package main

import (
	"errors"
	"math"
	"os"
	"time"

	"github.com/tkrajina/gpxgo/gpx"
	"github.com/s0ultr4d3r/psstelebot/tiles"
)

// LLT — одна точка GPX (lat/lon/time)
type LLT struct{ Lat, Lon float64; T time.Time }

// GpxTimeTrack — служебная структура для загрузки и расчётов (НЕ конфликтует с твоим Track)
type GpxTimeTrack struct {
	Pts      []LLT
	Start    time.Time
	End      time.Time
	Duration time.Duration
}

// loadGPXPath загружает GPX в GpxTimeTrack (служебный тип)
func loadGPXPath(path string) (*GpxTimeTrack, error) {
	f, err := os.Open(path)
	if err != nil { return nil, err }
	defer f.Close()

	doc, err := gpx.Parse(f)
	if err != nil { return nil, err }

	var pts []LLT
	var s, e time.Time
	for _, trk := range doc.Tracks {
		for _, seg := range trk.Segments {
			for _, p := range seg.Points {
				t := p.Timestamp
				pts = append(pts, LLT{Lat: p.Latitude, Lon: p.Longitude, T: t})
				if s.IsZero() || t.Before(s) { s = t }
				if e.IsZero() || t.After(e)  { e = t }
			}
		}
	}
	if len(pts) == 0 {
		return nil, errors.New("no points")
	}
	return &GpxTimeTrack{Pts: pts, Start: s, End: e, Duration: e.Sub(s)}, nil
}

// bboxOf возвращает (minLat,minLon,maxLat,maxLon) по набору служебных треков
func bboxOf(tracks []*GpxTimeTrack) (minLat, minLon, maxLat, maxLon float64) {
	minLat, minLon = +90, +180
	maxLat, maxLon = -90, -180
	for _, t := range tracks {
		for _, p := range t.Pts {
			if p.Lat < minLat { minLat = p.Lat }
			if p.Lat > maxLat { maxLat = p.Lat }
			if p.Lon < minLon { minLon = p.Lon }
			if p.Lon > maxLon { maxLon = p.Lon }
		}
	}
	return
}

// expandBBox — расширение bbox на k (0..0.5 обычно)
func expandBBox(minLat, minLon, maxLat, maxLon, k float64) (float64,float64,float64,float64) {
	return minLat-(maxLat-minLat)*k, minLon-(maxLon-minLon)*k, maxLat+(maxLat-minLat)*k, maxLon+(maxLon-minLon)*k
}

// pickZoom — подобрать зум, чтобы bbox влез в квадрат canvas×canvas
func pickZoom(minLat, minLon, maxLat, maxLon float64, canvas, tile int) int {
	for z := 20; z >= 0; z-- {
		x1 := tiles.MercX(minLon) * float64(tile) * math.Exp2(float64(z))
		x2 := tiles.MercX(maxLon) * float64(tile) * math.Exp2(float64(z))
		y1 := tiles.MercY(minLat) * float64(tile) * math.Exp2(float64(z))
		y2 := tiles.MercY(maxLat) * float64(tile) * math.Exp2(float64(z))
		w := math.Abs(x2 - x1)
		h := math.Abs(y2 - y1)
		if w <= float64(canvas) && h <= float64(canvas) {
			return z
		}
	}
	return 0
}

// Pt — экранный пиксель (определён в draw.go, дублирую сигнатуру)
//type Pt struct{ X, Y int }

// projectAll — проекция служебного трека в экранные целочисленные пиксели
func projectAll(t *GpxTimeTrack, z, tile int, originX, originY int, scale float64) []Pt {
	out := make([]Pt, 0, len(t.Pts))
	for _, p := range t.Pts {
		wx := tiles.MercX(p.Lon) * float64(tile) * math.Exp2(float64(z))
		wy := tiles.MercY(p.Lat) * float64(tile) * math.Exp2(float64(z))
		x := int(math.Round((wx - float64(originX)) * scale))
		y := int(math.Round((wy - float64(originY)) * scale))
		out = append(out, Pt{X: x, Y: y})
	}
	return out
}
