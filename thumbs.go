package main

import (
	"bufio"
	"bytes"
	"encoding/binary"
	"fmt"
	"image"
	"image/gif"
	"image/jpeg"
	"image/png"
	"io"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	_ "golang.org/x/image/bmp"
	"golang.org/x/image/draw"
	_ "golang.org/x/image/webp"
)

var _ = gif.Decode
var _ = png.Decode

const thumbMax = 360

var thumbExts = map[string]bool{"jpg": true, "jpeg": true, "png": true, "gif": true, "webp": true, "bmp": true}

// serveThumb returns a cached JPEG thumbnail, generating it on first request.
// The cache key changes when the file does, and browsers revalidate with the ETag.
func (a *App) serveThumb(w http.ResponseWriter, r *http.Request, abs string, info os.FileInfo) {
	ext := extOf(info.Name())
	if info.IsDir() || !thumbExts[ext] || info.Size() > 200<<20 {
		http.NotFound(w, r)
		return
	}
	key := sha(fmt.Sprintf("%s|%d|%d", abs, info.Size(), info.ModTime().UnixNano()))
	cache := filepath.Join(a.cfg.StateDir, "thumbs", key[:2], key+".jpg")
	w.Header().Set("Cache-Control", "private, max-age=86400")
	w.Header().Set("ETag", `"`+key[:32]+`"`)
	if serveCachedThumb(w, r, cache) {
		return
	}
	a.thumbSem <- struct{}{}
	defer func() { <-a.thumbSem }()
	if serveCachedThumb(w, r, cache) { // another request made it while this one waited
		return
	}
	data, err := makeThumb(abs)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	if err := os.MkdirAll(filepath.Dir(cache), 0o750); err == nil {
		if tmp, err := os.CreateTemp(filepath.Dir(cache), ".tmp-*"); err == nil {
			_, werr := tmp.Write(data)
			if cerr := tmp.Close(); werr == nil && cerr == nil {
				_ = os.Rename(tmp.Name(), cache)
			} else {
				_ = os.Remove(tmp.Name())
			}
		}
	}
	w.Header().Set("Content-Type", "image/jpeg")
	_, _ = w.Write(data)
}

// serveCachedThumb serves a cached thumbnail if there is one (answering 304 when the
// browser's copy is current) and marks it as recently used.
func serveCachedThumb(w http.ResponseWriter, r *http.Request, cache string) bool {
	f, err := os.Open(cache)
	if err != nil {
		return false
	}
	defer f.Close()
	fi, err := f.Stat()
	if err != nil {
		return false
	}
	if time.Since(fi.ModTime()) > 7*24*time.Hour { // at most one touch a week per thumbnail
		t := time.Now()
		_ = os.Chtimes(cache, t, t)
	}
	w.Header().Set("Content-Type", "image/jpeg")
	http.ServeContent(w, r, "", fi.ModTime(), f)
	return true
}

// pruneThumbs deletes thumbnails not used for maxAge (their file changed, was deleted,
// or nobody looked at it) and temp files from interrupted writes.
func (a *App) pruneThumbs(maxAge time.Duration) {
	cut := time.Now().Add(-maxAge)
	tmpCut := time.Now().Add(-time.Hour)
	_ = filepath.WalkDir(filepath.Join(a.cfg.StateDir, "thumbs"), func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return nil
		}
		if info.ModTime().Before(cut) || (strings.HasPrefix(d.Name(), ".tmp-") && info.ModTime().Before(tmpCut)) {
			_ = os.Remove(p)
		}
		return nil
	})
}

func makeThumb(abs string) ([]byte, error) {
	f, err := os.Open(abs)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	br := bufio.NewReaderSize(f, 1<<16)
	cfg, _, err := image.DecodeConfig(br)
	if err != nil {
		return nil, err
	}
	if cfg.Width <= 0 || cfg.Height <= 0 || cfg.Width*cfg.Height > 60_000_000 {
		return nil, fmt.Errorf("image too large")
	}
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		return nil, err
	}
	orient := 1
	if strings.HasSuffix(strings.ToLower(abs), ".jpg") || strings.HasSuffix(strings.ToLower(abs), ".jpeg") {
		head := make([]byte, 128<<10)
		n, _ := io.ReadFull(f, head)
		orient = exifOrientation(head[:n])
		if _, err := f.Seek(0, io.SeekStart); err != nil {
			return nil, err
		}
	}
	src, _, err := image.Decode(bufio.NewReaderSize(f, 1<<16))
	if err != nil {
		return nil, err
	}
	b := src.Bounds()
	w, h := b.Dx(), b.Dy()
	scale := float64(thumbMax) / float64(max(w, h))
	if scale > 1 {
		scale = 1
	}
	tw, th := max(1, int(float64(w)*scale)), max(1, int(float64(h)*scale))
	dst := image.NewRGBA(image.Rect(0, 0, tw, th))
	draw.Draw(dst, dst.Bounds(), image.White, image.Point{}, draw.Src) // flatten transparency onto white
	draw.BiLinear.Scale(dst, dst.Bounds(), src, b, draw.Over, nil)
	out := orientImage(dst, orient)
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, out, &jpeg.Options{Quality: 80}); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// exifOrientation reads the EXIF orientation tag (1-8) from the start of a JPEG.
func exifOrientation(b []byte) int {
	if len(b) < 4 || b[0] != 0xFF || b[1] != 0xD8 {
		return 1
	}
	i := 2
	for i+4 <= len(b) {
		if b[i] != 0xFF {
			return 1
		}
		marker := b[i+1]
		size := int(binary.BigEndian.Uint16(b[i+2:]))
		if marker == 0xE1 && i+4+size <= len(b) && size > 14 && string(b[i+4:i+10]) == "Exif\x00\x00" {
			t := b[i+10 : i+2+size]
			var bo binary.ByteOrder
			switch string(t[:2]) {
			case "II":
				bo = binary.LittleEndian
			case "MM":
				bo = binary.BigEndian
			default:
				return 1
			}
			off := int(bo.Uint32(t[4:]))
			if off+2 > len(t) {
				return 1
			}
			n := int(bo.Uint16(t[off:]))
			for k := 0; k < n; k++ {
				e := off + 2 + k*12
				if e+12 > len(t) {
					return 1
				}
				if bo.Uint16(t[e:]) == 0x0112 {
					v := int(bo.Uint16(t[e+8:]))
					if v >= 1 && v <= 8 {
						return v
					}
					return 1
				}
			}
			return 1
		}
		if marker == 0xDA {
			return 1
		}
		i += 2 + size
	}
	return 1
}

func orientImage(src *image.RGBA, o int) image.Image {
	if o <= 1 || o > 8 {
		return src
	}
	b := src.Bounds()
	w, h := b.Dx(), b.Dy()
	var dst *image.RGBA
	if o >= 5 {
		dst = image.NewRGBA(image.Rect(0, 0, h, w))
	} else {
		dst = image.NewRGBA(image.Rect(0, 0, w, h))
	}
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			var dx, dy int
			switch o {
			case 2:
				dx, dy = w-1-x, y
			case 3:
				dx, dy = w-1-x, h-1-y
			case 4:
				dx, dy = x, h-1-y
			case 5:
				dx, dy = y, x
			case 6:
				dx, dy = h-1-y, x
			case 7:
				dx, dy = h-1-y, w-1-x
			case 8:
				dx, dy = y, w-1-x
			}
			dst.Set(dx, dy, src.At(x, y))
		}
	}
	return dst
}
