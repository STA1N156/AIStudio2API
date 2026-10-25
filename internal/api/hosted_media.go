package api

import (
	"crypto/sha256"
	"encoding/hex"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"sync"
	"time"

	"github.com/Mag1cFall/AIStudio2API/internal/aistudio"
)

const (
	// hostedMediaRetention 是生成媒体链接的有效期，过期文件在下次清理时删除
	hostedMediaRetention = 24 * time.Hour
	// hostedMediaMaxBytes 是生成媒体目录的总量上限，超出时先删最旧的文件
	hostedMediaMaxBytes = 5 << 30
	// hostedMediaCleanupInterval 是两次清理之间的最短间隔
	hostedMediaCleanupInterval = time.Hour
)

// hostedMediaTypes 是可托管的媒体类型与文件扩展名，其他类型仍以 data URI 返回
var hostedMediaTypes = map[string]string{
	"image/png": ".png", "image/jpeg": ".jpg", "image/webp": ".webp", "image/gif": ".gif",
	"audio/wav": ".wav", "audio/mpeg": ".mp3", "audio/ogg": ".ogg", "audio/aac": ".aac", "audio/flac": ".flac",
	"video/mp4": ".mp4", "video/webm": ".webm", "video/quicktime": ".mov",
}

// hostedMediaName 是托管文件名：内容 SHA-256 前 128 位的十六进制加扩展名，内容相同的媒体共用一个文件
var hostedMediaName = regexp.MustCompile(`^[0-9a-f]{32}\.[a-z0-9]+$`)

// hostedMediaURL 匹配本服务签发的媒体链接
var hostedMediaURL = regexp.MustCompile(`^https?://[^/\s]+/media/([0-9a-f]{32}\.[a-z0-9]+)$`)

// hostedMedia 是进程内的生成媒体目录，未配置时为 nil，输出保持 base64
var hostedMedia *mediaStore

// mediaStore 把生成的图片、音频、视频写入目录，供 /media/{name} 公开读取
type mediaStore struct {
	dir         string
	mu          sync.Mutex
	lastCleanup time.Time
}

// newMediaStore 创建媒体目录，失败时返回 nil 表示不托管
func newMediaStore(dir string) *mediaStore {
	if dir == "" || os.MkdirAll(dir, 0o755) != nil {
		return nil
	}
	return &mediaStore{dir: dir}
}

// save 写入媒体并返回文件名；L16 音频先转为 WAV，客户端才能直接播放
func (store *mediaStore) save(media aistudio.Media) (string, bool) {
	data, mimeType := media.Data, media.MIME
	baseType, _, err := mime.ParseMediaType(mimeType)
	if err != nil {
		return "", false
	}
	if baseType == "audio/l16" || baseType == "audio/pcm" {
		if data, _, err = encodeSpeechResponse(media, "wav"); err != nil {
			return "", false
		}
		baseType = "audio/wav"
	}
	extension, ok := hostedMediaTypes[baseType]
	if !ok || len(data) == 0 {
		return "", false
	}
	sum := sha256.Sum256(data)
	name := hex.EncodeToString(sum[:16]) + extension
	path := filepath.Join(store.dir, name)
	now := time.Now()
	if os.Chtimes(path, now, now) != nil {
		temporary, err := os.CreateTemp(store.dir, ".media-*")
		if err != nil {
			return "", false
		}
		_, writeErr := temporary.Write(data)
		closeErr := temporary.Close()
		if writeErr != nil || closeErr != nil || os.Rename(temporary.Name(), path) != nil {
			_ = os.Remove(temporary.Name())
			return "", false
		}
	}
	store.cleanupLater(now)
	return name, true
}

// cleanupLater 距上次清理超过间隔时在后台删除过期与超量的文件
func (store *mediaStore) cleanupLater(now time.Time) {
	store.mu.Lock()
	due := now.Sub(store.lastCleanup) >= hostedMediaCleanupInterval
	if due {
		store.lastCleanup = now
	}
	store.mu.Unlock()
	if due {
		go store.cleanup(now)
	}
}

// cleanup 删除超过有效期的文件，总量仍超上限时从最旧的开始删
func (store *mediaStore) cleanup(now time.Time) {
	entries, err := os.ReadDir(store.dir)
	if err != nil {
		return
	}
	type file struct {
		path     string
		size     int64
		modified time.Time
	}
	var kept []file
	var total int64
	for _, entry := range entries {
		info, err := entry.Info()
		if err != nil || !info.Mode().IsRegular() {
			continue
		}
		path := filepath.Join(store.dir, entry.Name())
		if now.Sub(info.ModTime()) > hostedMediaRetention {
			_ = os.Remove(path)
			continue
		}
		kept = append(kept, file{path, info.Size(), info.ModTime()})
		total += info.Size()
	}
	slices.SortFunc(kept, func(left, right file) int { return left.modified.Compare(right.modified) })
	for _, item := range kept {
		if total <= hostedMediaMaxBytes {
			break
		}
		if os.Remove(item.path) == nil {
			total -= item.size
		}
	}
}

// read 读取本服务签发的媒体链接对应的文件，链接不是本服务签发或文件已过期时返回 false
func (store *mediaStore) read(url string) (aistudio.Blob, bool) {
	match := hostedMediaURL.FindStringSubmatch(url)
	if store == nil || match == nil {
		return aistudio.Blob{}, false
	}
	data, err := os.ReadFile(filepath.Join(store.dir, match[1]))
	if err != nil {
		return aistudio.Blob{}, false
	}
	return aistudio.Blob{MIME: hostedMediaType(match[1]), Data: data}, true
}

// hostedMediaType 由托管文件扩展名得到 MIME
func hostedMediaType(name string) string {
	extension := filepath.Ext(name)
	for mimeType, candidate := range hostedMediaTypes {
		if candidate == extension {
			return mimeType
		}
	}
	return "application/octet-stream"
}

// mediaPublish 把媒体内容换成本服务的公开链接；为 nil 时保持原样以 data URI 输出
type mediaPublish func(aistudio.Media) aistudio.Media

func (publish mediaPublish) apply(media aistudio.Media) aistudio.Media {
	if publish == nil {
		return media
	}
	return publish(media)
}

// mediaPublisher 返回本次请求的媒体链接生成器，链接主机取自客户端访问本服务使用的地址
func (s *server) mediaPublisher(r *http.Request) mediaPublish {
	if s.media == nil {
		return nil
	}
	origin := requestOrigin(r)
	return func(media aistudio.Media) aistudio.Media {
		if len(media.Data) == 0 {
			return media
		}
		name, ok := s.media.save(media)
		if !ok {
			return media
		}
		return aistudio.Media{MIME: hostedMediaType(name), URL: origin + "/media/" + name, Name: media.Name}
	}
}

// handleHostedMedia 公开返回生成的媒体文件；文件名含 128 位内容摘要，无法猜测，不需要 API 密钥
func (s *server) handleHostedMedia(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if !hostedMediaName.MatchString(name) {
		http.NotFound(w, r)
		return
	}
	file, err := os.Open(filepath.Join(s.media.dir, name))
	if err != nil {
		http.NotFound(w, r)
		return
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		http.NotFound(w, r)
		return
	}
	header := w.Header()
	header.Set("Content-Type", hostedMediaType(name))
	header.Set("Cache-Control", "public, max-age=86400, immutable")
	header.Set("Access-Control-Allow-Origin", "*")
	http.ServeContent(w, r, name, info.ModTime(), file)
}
