package main

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/bogem/id3v2/v2"
)

type fakeCredentialStore struct {
	token string
}

func (f fakeCredentialStore) SaveInteractively() error { return nil }
func (f fakeCredentialStore) Load() (string, error)    { return f.token, nil }
func (f fakeCredentialStore) Delete() error            { return nil }

func TestDownloadRetriesTagsAndSkipsExisting(t *testing.T) {
	t.Parallel()
	audioRequests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/cover.jpg":
			w.Header().Set("Content-Type", "image/jpeg")
			_, _ = w.Write([]byte{0xff, 0xd8, 0xff, 0xdb, 0x00, 0x43, 0x00})
		case "/audio/test.mp3":
			audioRequests++
			if audioRequests == 1 {
				w.WriteHeader(http.StatusInternalServerError)
				return
			}
			w.Header().Set("Content-Type", "audio/mpeg")
			_, _ = w.Write(fakeMP3())
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	client := newAPIClient()
	client.baseURL = server.URL
	client.imageURL = server.URL
	client.audioURL = server.URL + "/audio"
	d := downloader{
		api:     client,
		creds:   fakeCredentialStore{},
		out:     io.Discard,
		err:     io.Discard,
		retries: 2,
		sleep:   func(context.Context, time.Duration) error { return nil },
	}
	book := &album{ID: "1", Title: "测试专辑", Author: "测试作者", Cover: "cover.jpg", Chapters: []chapter{{
		ID: "10", Title: "标题/一", Media: media{Audio: "test.mp3", MediaType: "audio"},
	}}}
	dir := t.TempDir()
	result := d.downloadAlbum(context.Background(), book, dir)
	if len(result.failed) != 0 || result.downloaded != 1 || audioRequests != 2 {
		t.Fatalf("result=%+v requests=%d", result, audioRequests)
	}
	target := filepath.Join(dir, "001 - 标题_一.mp3")
	tag, err := id3v2.Open(target, id3v2.Options{Parse: true})
	if err != nil {
		t.Fatal(err)
	}
	defer tag.Close()
	if tag.Title() != "标题/一" || tag.Album() != "测试专辑" || tag.Artist() != "测试作者" {
		t.Fatalf("ID3 标签不正确: title=%q album=%q artist=%q", tag.Title(), tag.Album(), tag.Artist())
	}
	if frames := tag.GetFrames("TRCK"); len(frames) != 1 {
		t.Fatalf("TRCK frames = %d", len(frames))
	}

	result = d.downloadAlbum(context.Background(), book, dir)
	if result.skipped != 1 || audioRequests != 2 {
		t.Fatalf("未跳过已存在文件: result=%+v requests=%d", result, audioRequests)
	}
}

func TestConvertToMP3InvokesConfiguredFFmpeg(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell fixture")
	}
	dir := t.TempDir()
	fakeFFmpeg := filepath.Join(dir, "ffmpeg")
	script := "#!/bin/sh\ncp \"$7\" \"${15}\"\n"
	if err := os.WriteFile(fakeFFmpeg, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	source := filepath.Join(dir, "source.m4a")
	target := filepath.Join(dir, "target.mp3")
	if err := os.WriteFile(source, []byte("audio"), 0o600); err != nil {
		t.Fatal(err)
	}
	d := downloader{ffmpeg: fakeFFmpeg}
	if err := d.convertToMP3(context.Background(), source, target); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(target)
	if err != nil || string(data) != "audio" {
		t.Fatalf("target=%q err=%v", data, err)
	}
}

func TestSanitizeFilename(t *testing.T) {
	t.Parallel()
	cases := map[string]string{
		`  A/B:C*D?  `: "A_B_C_D_",
		"\n\t":         "未命名章节",
		"普通标题":         "普通标题",
	}
	for input, want := range cases {
		if got := sanitizeFilename(input); got != want {
			t.Errorf("sanitizeFilename(%q)=%q, want %q", input, got, want)
		}
	}
	long := strings.Repeat("章", 100)
	if got := sanitizeFilename(long); len(got) > 180 {
		t.Fatalf("文件名过长: %d", len(got))
	}
}

func fakeMP3() []byte {
	// ID3 库只需一个可读写文件；帧同步头让夹具也能被常见探测器识别为 MPEG 音频。
	return append([]byte{0xff, 0xfb, 0x90, 0x64}, make([]byte, 4096)...)
}

func TestParseDownloadOptions(t *testing.T) {
	t.Parallel()
	options, err := parseDownloadOptions([]string{"-album", "64", "-retries", "4"})
	if err != nil {
		t.Fatal(err)
	}
	if options.albumID != "64" || options.output != "output-64" || options.retries != 4 {
		t.Fatalf("options=%+v", options)
	}
	if _, err := parseDownloadOptions([]string{"-album", "../../etc"}); err == nil {
		t.Fatal("expected invalid ID error")
	}
}

func Example_sanitizeFilename() {
	fmt.Println(sanitizeFilename(`标题/上:第一节`))
	// Output: 标题_上_第一节
}
