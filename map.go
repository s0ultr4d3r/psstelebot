package main

import (
	"bytes"
	"context"
	"fmt"
	"image"
	"image/jpeg"
	"image/png"
	"io"
	"net/http"
	"strings"
	"time"
)

func expandStaticURL(tpl string, bb boundsLL, w, h int) string {
	repl := map[string]string{
		"{minLon}": fmt.Sprintf("%.6f", bb.minLon),
		"{minLat}": fmt.Sprintf("%.6f", bb.minLat),
		"{maxLon}": fmt.Sprintf("%.6f", bb.maxLon),
		"{maxLat}": fmt.Sprintf("%.6f", bb.maxLat),
		"{w}":      fmt.Sprintf("%d", w),
		"{h}":      fmt.Sprintf("%d", h),
	}
	out := tpl
	for k, v := range repl {
		out = strings.ReplaceAll(out, k, v)
	}
	return strings.TrimSpace(out)
}

func fetchStaticMap(ctx context.Context, url string) (image.Image, error) {
	url = strings.TrimSpace(url)
	client := &http.Client{Timeout: 30 * time.Second}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil { return nil, err }
	resp, err := client.Do(req)
	if err != nil { return nil, err }
	defer resp.Body.Close()

	if resp.StatusCode/100 != 2 {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<10))
		return nil, fmt.Errorf("map HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(b)))
	}
	buf, err := io.ReadAll(resp.Body)
	if err != nil { return nil, err }
	if img, err := png.Decode(bytes.NewReader(buf)); err == nil { return img, nil }
	return jpeg.Decode(bytes.NewReader(buf))
}
