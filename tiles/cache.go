package tiles

import (
	"bytes"
	"crypto/sha1"
	"fmt"
	"image"
	"image/png"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"time"

	lru "github.com/hashicorp/golang-lru/v2"
)

// Loader provides aggressive HTTP keep-alive + on-disk and in-memory caching
// of decoded PNG tiles. Keep exactly one instance per process.
type Loader struct {
	Client  *http.Client
	DiskDir string
	memLRU  *lru.Cache[string, image.Image]

	muDecode sync.Mutex // avoid duplicate decoding
}

func NewLoader(diskDir string, memItems int) *Loader {
	tr := &http.Transport{
		MaxIdleConns:        1024,
		MaxIdleConnsPerHost: 256,
		MaxConnsPerHost:     256,
		IdleConnTimeout:     90 * time.Second,
		ForceAttemptHTTP2:   true,
		DisableCompression:  false,
	}
	client := &http.Client{Transport: tr, Timeout: 25 * time.Second}

	mem, _ := lru.New[string, image.Image](memItems)
	_ = os.MkdirAll(diskDir, 0o755)

	return &Loader{
		Client:  client,
		DiskDir: diskDir,
		memLRU:  mem,
	}
}

func (l *Loader) key(url string) string {
	sum := sha1.Sum([]byte(url))
	return fmt.Sprintf("%x", sum[:])
}

func (l *Loader) path(key string) string {
	return filepath.Join(l.DiskDir, key[:2], key[2:4], key+".png")
}

func (l *Loader) getBytes(url string) ([]byte, error) {
	if im, ok := l.memLRU.Get(url); ok && im != nil {
		// already decoded in RAM: fast-path — re-encode on the fly is not needed,
		// but we still need bytes only when PNG decoding failed earlier.
	}
	k := l.key(url)
	p := l.path(k)
	if b, err := os.ReadFile(p); err == nil {
		return b, nil
	}
	resp, err := l.Client.Get(url)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		io.Copy(io.Discard, resp.Body)
		return nil, fmt.Errorf("tile %s: %s", url, resp.Status)
	}
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	_ = os.MkdirAll(filepath.Dir(p), 0o755)
	_ = os.WriteFile(p, b, 0o644)
	return b, nil
}

func (l *Loader) GetImage(url string) (image.Image, error) {
	if im, ok := l.memLRU.Get(url); ok {
		return im, nil
	}
	b, err := l.getBytes(url)
	if err != nil {
		return nil, err
	}
	l.muDecode.Lock()
	defer l.muDecode.Unlock()
	// second-look to avoid duplicate decoding
	if im, ok := l.memLRU.Get(url); ok {
		return im, nil
	}
	im, err := png.Decode(bytes.NewReader(b))
	if err != nil {
		return nil, err
	}
	l.memLRU.Add(url, im)
	return im, nil
}
