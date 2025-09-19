package main

import (
	"encoding/xml"
	"fmt"
	"os"
	"time"
)

type PtLL struct {
	Lat float64
	Lon float64
	T   *time.Time
}

type gpxFile struct {
	Trk []trk `xml:"trk"`
}
type trk struct {
	Seg []trkseg `xml:"trkseg"`
}
type trkseg struct {
	Pt []wpt `xml:"trkpt"`
}
type wpt struct {
	Lat  float64    `xml:"lat,attr"`
	Lon  float64    `xml:"lon,attr"`
	Time *time.Time `xml:"time"`
}

func ParseGPXFile(path string) ([]PtLL, error) {
	f, err := os.Open(path)
	if err != nil { return nil, fmt.Errorf("open: %w", err) }
	defer f.Close()

	var g gpxFile
	if err := xml.NewDecoder(f).Decode(&g); err != nil {
		return nil, fmt.Errorf("decode: %w", err)
	}
	out := make([]PtLL, 0, 1024)
	for _, tr := range g.Trk {
		for _, s := range tr.Seg {
			for _, p := range s.Pt {
				out = append(out, PtLL{Lat: p.Lat, Lon: p.Lon, T: p.Time})
			}
		}
	}
	return out, nil
}
