package main

import (
	"context"
	"fmt"
	"image"
	"image/jpeg"
	"image/png"
	"net/http"
	"strings"
	"time"

	"github.com/tkrajina/gpxgo/gpx"
)

// PtLL — точка «широта/долгота/время» (T — *time.Time, как у тебя в HUD)
type PtLL struct {
	Lat, Lon float64
	T        *time.Time
}

// ParseGPXFile → []PtLL (lat/lon/time)
func ParseGPXFile(path string) ([]PtLL, error) {
	doc, err := gpx.ParseFile(path)
	if err != nil {
		return nil, err
	}
	out := make([]PtLL, 0, 1024)
	for _, trk := range doc.Tracks {
		for _, seg := range trk.Segments {
			for _, p := range seg.Points {
				t := p.Timestamp
				out = append(out, PtLL{Lat: p.Latitude, Lon: p.Longitude, T: &t})
			}
		}
	}
	return out, nil
}

// boundsLL — bbox в градусах
type boundsLL struct {
	minLat, minLon, maxLat, maxLon float64
}

func bboxLL(pts []PtLL) boundsLL {
	b := boundsLL{minLat: +90, minLon: +180, maxLat: -90, maxLon: -180}
	for _, p := range pts {
		if p.Lat < b.minLat {
			b.minLat = p.Lat
		}
		if p.Lat > b.maxLat {
			b.maxLat = p.Lat
		}
		if p.Lon < b.minLon {
			b.minLon = p.Lon
		}
		if p.Lon > b.maxLon {
			b.maxLon = p.Lon
		}
	}
	return b
}

// expandStaticURL — подстановка плейсхолдеров для статической карты
// шаблон поддерживает: {minLon},{minLat},{maxLon},{maxLat},{w},{h}
func expandStaticURL(tmpl string, bb boundsLL, w, h int) string {
	r := strings.NewReplacer(
		"{minLon}", fmt.Sprintf("%.6f", bb.minLon),
		"{minLat}", fmt.Sprintf("%.6f", bb.minLat),
		"{maxLon}", fmt.Sprintf("%.6f", bb.maxLon),
		"{maxLat}", fmt.Sprintf("%.6f", bb.maxLat),
		"{w}", fmt.Sprintf("%d", w),
		"{h}", fmt.Sprintf("%d", h),
	)
	return r.Replace(tmpl)
}

// fetchStaticMap — качает PNG/JPEG по URL с контекстом
func fetchStaticMap(ctx context.Context, url string) (image.Image, error) {
	client := &http.Client{Timeout: 20 * time.Second}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	ct := strings.ToLower(resp.Header.Get("Content-Type"))
	switch {
	case strings.Contains(ct, "png"):
		return png.Decode(resp.Body)
	case strings.Contains(ct, "jpeg"), strings.Contains(ct, "jpg"):
		return jpeg.Decode(resp.Body)
	default:
		// пробуем оба
		if im, err2 := png.Decode(resp.Body); err2 == nil {
			return im, nil
		}
		return jpeg.Decode(resp.Body)
	}
}
