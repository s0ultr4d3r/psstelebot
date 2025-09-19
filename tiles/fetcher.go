package tiles

import (
	"context"
	"crypto/sha1"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"golang.org/x/time/rate"
)

type Fetcher struct {
	Client     *http.Client
	Limiter    *rate.Limiter
	CacheDir   string
	UserAgent  string
	MaxRetries int
}

func NewFetcher(cacheDir string, rps float64, burst int, timeout time.Duration) (*Fetcher, error) {
	if cacheDir == "" {
		cacheDir = ".tile-cache"
	}
	if err := os.MkdirAll(cacheDir, 0o755); err != nil {
		return nil, err
	}
	return &Fetcher{
		Client: &http.Client{
			Timeout: timeout,
		},
		Limiter:    rate.NewLimiter(rate.Limit(rps), burst),
		CacheDir:   cacheDir,
		UserAgent:  "gpx2gif/1.0 (+tiles; https://openstreetmap.org)",
		MaxRetries: 3,
	}, nil
}

func (f *Fetcher) cachePath(u string) string {
	sum := sha1.Sum([]byte(u))
	hexid := hex.EncodeToString(sum[:])
	ext := ".tile"
	if i := strings.IndexByte(u, '?'); i >= 0 {
		u = u[:i]
	}
	if j := strings.LastIndexByte(u, '.'); j >= 0 && j > len(u)-6 {
		ext = u[j:]
		if len(ext) > 5 {
			ext = ".tile"
		}
	}
	return filepath.Join(f.CacheDir, hexid[:2], hexid[2:4], hexid+ext)
}

func (f *Fetcher) readFromCache(cp string) ([]byte, string, error) {
	b, err := os.ReadFile(cp)
	if err != nil {
		return nil, "", err
	}
	ct := ""
	if ctb, err2 := os.ReadFile(cp + ".ct"); err2 == nil {
		ct = string(ctb)
	}
	return b, ct, nil
}

func (f *Fetcher) GetTile(ctx context.Context, url string, headers map[string]string) ([]byte, string, error) {
	cp := f.cachePath(url)
	if b, ct, err := f.readFromCache(cp); err == nil {
		return b, ct, nil
	}
	if err := os.MkdirAll(filepath.Dir(cp), 0o755); err != nil {
		return nil, "", err
	}

	var lastErr error
	for attempt := 0; attempt < f.MaxRetries; attempt++ {
		if err := f.Limiter.Wait(ctx); err != nil {
			return nil, "", err
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
		if err != nil {
			return nil, "", err
		}
		req.Header.Set("User-Agent", f.UserAgent)
		for k, v := range headers {
			req.Header.Set(k, v)
		}

		resp, err := f.Client.Do(req)
		if err != nil {
			lastErr = err
			time.Sleep(time.Duration(200+attempt*200) * time.Millisecond)
			continue
		}
		func() {
			defer resp.Body.Close()
			if resp.StatusCode != 200 {
				lastErr = fmt.Errorf("tile HTTP %d for %s", resp.StatusCode, url)
				return
			}
			body, err := io.ReadAll(resp.Body)
			if err != nil {
				lastErr = err
				return
			}
			tmp := cp + ".tmp"
			if err := os.WriteFile(tmp, body, 0o644); err != nil {
				lastErr = err
				return
			}
			ct := resp.Header.Get("Content-Type")
			_ = os.WriteFile(cp+".ct", []byte(ct), 0o644)
			lastErr = os.Rename(tmp, cp)
		}()
		if lastErr == nil {
			data, _ := os.ReadFile(cp)
			ct := ""
			if b, err := os.ReadFile(cp + ".ct"); err == nil {
				ct = string(b)
			}
			return data, ct, nil
		}
		time.Sleep(time.Duration(400+attempt*250) * time.Millisecond)
	}
	return nil, "", lastErr
}

func (f *Fetcher) URLFor(p Preset, z, x, y int) (string, map[string]string, error) {
	u, err := p.FillURL(z, x, y)
	if err != nil {
		return "", nil, err
	}
	return u, p.Headers, nil
}

func ClampZoom(z int, p Preset) int {
	if z < p.MinZoom {
		return p.MinZoom
	}
	if z > p.MaxZoom {
		return p.MaxZoom
	}
	return z
}

func (f *Fetcher) SaveTile(ctx context.Context, url string, headers map[string]string, out string) error {
	data, _, err := f.GetTile(ctx, url, headers)
	if err != nil {
		return err
	}
	if out == "" {
		return errors.New("out path required")
	}
	if err := os.MkdirAll(filepath.Dir(out), 0o755); err != nil {
		return err
	}
	return os.WriteFile(out, data, 0o644)
}
