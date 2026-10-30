package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/Mag1cFall/AIStudio2API/internal/aistudio"
)

type mediaService struct{ media aistudio.Media }

func (s mediaService) Models(context.Context) ([]aistudio.Model, error) { return nil, nil }

func (s mediaService) CountTokens(context.Context, aistudio.TokenCountRequest) (aistudio.TokenCount, error) {
	return aistudio.TokenCount{}, nil
}

func (s mediaService) Generate(context.Context, aistudio.GenerateRequest) (<-chan aistudio.Event, error) {
	events := make(chan aistudio.Event, 2)
	media := s.media
	events <- aistudio.Event{Kind: aistudio.EventMedia, Media: &media}
	events <- aistudio.Event{Kind: aistudio.EventFinish, FinishReason: "stop"}
	close(events)
	return events, nil
}

var hostedLinkPattern = regexp.MustCompile(`!\[image\]\((https://example\.com/media/[0-9a-f]{32}\.png)\)`)

func TestChatReturnsHostedMediaLinks(t *testing.T) {
	image := []byte("\x89PNG\r\n\x1a\nhosted-media-test")
	s := &server{thoughtSignatures: newThoughtSignatureStore(), service: mediaService{aistudio.Media{MIME: "image/png", Data: image}}, media: newMediaStore(t.TempDir())}
	previous := hostedMedia
	hostedMedia = s.media
	defer func() { hostedMedia = previous }()

	var link string
	for _, stream := range []bool{false, true} {
		w := httptest.NewRecorder()
		r := httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader(
			`{"model":"test","stream":`+map[bool]string{false: "false", true: "true"}[stream]+`,"messages":[{"role":"user","content":"draw"}]}`))
		r.Host = "internal:2048"
		r.Header.Set("X-Forwarded-Proto", "https")
		r.Header.Set("X-Forwarded-Host", "example.com")
		s.handleChatCompletions(w, r)
		match := hostedLinkPattern.FindStringSubmatch(w.Body.String())
		if w.Code != http.StatusOK || match == nil || strings.Contains(w.Body.String(), "base64") {
			t.Fatalf("stream=%v: %d %s", stream, w.Code, w.Body.String())
		}
		if link != "" && match[1] != link {
			t.Fatalf("same content got different links: %s %s", link, match[1])
		}
		link = match[1]
	}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /media/{name}", s.handleHostedMedia)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, httptest.NewRequest("GET", strings.TrimPrefix(link, "https://example.com"), nil))
	if w.Code != http.StatusOK || w.Header().Get("Content-Type") != "image/png" || w.Body.String() != string(image) {
		t.Fatalf("media download = %d %q", w.Code, w.Header().Get("Content-Type"))
	}
	w = httptest.NewRecorder()
	mux.ServeHTTP(w, httptest.NewRequest("GET", "/media/..%2Fsecret.png", nil))
	if w.Code != http.StatusNotFound {
		t.Fatalf("invalid name = %d", w.Code)
	}

	history, _ := json.Marshal("here ![image](" + link + ") and ![image](https://example.com/media/" + strings.Repeat("0", 32) + ".png)")
	parts, err := openAIAssistantContentParts(history, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(parts) != 3 || parts[1].InlineData == nil || string(parts[1].InlineData.Data) != string(image) ||
		!strings.Contains(parts[2].Text, strings.Repeat("0", 32)) {
		t.Fatalf("history parts = %+v", parts)
	}
}

func TestMediaLinksPreferPublicBaseURL(t *testing.T) {
	s := &server{config: Config{PublicBaseURL: "https://ai.example.com"}, media: newMediaStore(t.TempDir())}
	r := httptest.NewRequest("POST", "/v1/chat/completions", nil)
	r.Host = "aistudio2api.zeabur.internal:2048"
	media := s.mediaPublisher(r).apply(aistudio.Media{MIME: "image/jpeg", Data: []byte("jpeg")})
	if !regexp.MustCompile(`^https://ai\.example\.com/media/[0-9a-f]{32}\.jpg$`).MatchString(media.URL) || media.Data != nil {
		t.Fatalf("media url = %q", media.URL)
	}
	s.config.PublicBaseURL = ""
	if media := s.mediaPublisher(r).apply(aistudio.Media{MIME: "image/jpeg", Data: []byte("jpeg")}); !strings.HasPrefix(media.URL, "http://aistudio2api.zeabur.internal:2048/media/") {
		t.Fatalf("fallback media url = %q", media.URL)
	}
}

func TestMediaStoreConvertsPCMAndCleansUp(t *testing.T) {
	store := newMediaStore(t.TempDir())
	name, ok := store.save(aistudio.Media{MIME: "audio/L16;codec=pcm;rate=24000", Data: make([]byte, 480)})
	if !ok || !strings.HasSuffix(name, ".wav") {
		t.Fatalf("pcm name = %q %v", name, ok)
	}
	data, err := os.ReadFile(filepath.Join(store.dir, name))
	if err != nil || string(data[:4]) != "RIFF" {
		t.Fatalf("pcm was not stored as wav: %v", err)
	}
	if _, ok := store.save(aistudio.Media{MIME: "text/plain", Data: []byte("x")}); ok {
		t.Fatal("unsupported media type was hosted")
	}

	old := filepath.Join(store.dir, strings.Repeat("a", 32)+".png")
	if err := os.WriteFile(old, []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}
	past := time.Now().Add(-hostedMediaRetention - time.Minute)
	if err := os.Chtimes(old, past, past); err != nil {
		t.Fatal(err)
	}
	store.cleanup(time.Now())
	if _, err := os.Stat(old); !os.IsNotExist(err) {
		t.Fatal("expired media was kept")
	}
	if _, err := os.Stat(filepath.Join(store.dir, name)); err != nil {
		t.Fatal("fresh media was removed")
	}
}
