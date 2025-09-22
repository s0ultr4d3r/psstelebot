package main

import (
	"fmt"
	"image" // ← алиас, чтобы ничего не затенялось
	"net/http"
	"strings"
	"time"
)

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

// Вернём stdimg.Image (пока заглушка — статическая карта не используется в твоём запуске)
func fetchStaticMap(_ interface{}, url string) (image.Image, error) {
	return nil, fmt.Errorf("staticURL is not implemented: %s", url)
}

func newHTTPClient(timeout time.Duration) *http.Client {
	return &http.Client{Timeout: timeout}
}
