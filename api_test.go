package main

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestFetchAlbumPreservesPublishedOrder(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/gapi/v1/albums/64" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{
          "data":{"id":"64","type":"albums","attributes":{"title":"专辑","author":"作者","cover":"cover.jpg"},"relationships":{"published-audiobooks":{"data":[{"type":"radios","id":"2"},{"type":"radios","id":"1"}]}}},
          "included":[
            {"id":"1","type":"radios","attributes":{"title":"第一章"},"relationships":{"media":{"data":{"type":"medias","id":"m1"}}}},
            {"id":"2","type":"radios","attributes":{"title":"第二章"},"relationships":{"media":{"data":{"type":"medias","id":"m2"}}}},
            {"id":"m1","type":"medias","attributes":{"audio":"one.mp3","media-type":"audio"}},
            {"id":"m2","type":"medias","attributes":{"audio":"two.mp3","media-type":"protected_audio"}}
          ]}`)
	}))
	defer server.Close()

	client := newAPIClient()
	client.baseURL = server.URL
	book, err := client.fetchAlbum(context.Background(), "64")
	if err != nil {
		t.Fatal(err)
	}
	if got, want := len(book.Chapters), 2; got != want {
		t.Fatalf("章节数 = %d, want %d", got, want)
	}
	if book.Chapters[0].ID != "2" || book.Chapters[1].ID != "1" {
		t.Fatalf("未保留 published-audiobooks 顺序: %#v", book.Chapters)
	}
}

func TestFetchAlbumFallsBackToRadioEndpoint(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/gapi/v1/albums/7":
			fmt.Fprint(w, `{"data":{"id":"7","type":"albums","attributes":{"title":"A","author":"B","cover":"c.jpg"},"relationships":{"published-audiobooks":{"data":[{"type":"radios","id":"9"}]}}},"included":[]}`)
		case "/gapi/v1/radios/9":
			fmt.Fprint(w, `{"data":{"id":"9","type":"radios","attributes":{"title":"C"},"relationships":{"media":{"data":{"type":"medias","id":"10"}}}},"included":[{"id":"10","type":"medias","attributes":{"audio":"a.mp3","media-type":"audio"}}]}`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	client := newAPIClient()
	client.baseURL = server.URL
	book, err := client.fetchAlbum(context.Background(), "7")
	if err != nil {
		t.Fatal(err)
	}
	if got := book.Chapters[0].Media.Audio; got != "a.mp3" {
		t.Fatalf("audio = %q", got)
	}
}

func TestAuthorizedMediaURLUsesCurrentWebFlow(t *testing.T) {
	t.Parallel()
	const token = "sensitive-token"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/gapi/v1/medias/protected/radios/123" {
			t.Fatalf("path = %q", r.URL.Path)
		}
		if got := r.URL.Query().Get("format"); got != "json" {
			t.Fatalf("format = %q", got)
		}
		if got, want := r.Header.Get("Authorization"), "Token token="+token; got != want {
			t.Fatalf("Authorization = %q", got)
		}
		cookie, err := r.Cookie("appToken")
		if err != nil || cookie.Value != token {
			t.Fatalf("appToken cookie = %#v, err=%v", cookie, err)
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"url":%q}`, serverURL(r)+"/signed/audio.mp3?secret=redacted")
	}))
	defer server.Close()

	client := newAPIClient()
	client.baseURL = server.URL
	got, err := client.authorizedMediaURL(context.Background(), "123", token)
	if err != nil {
		t.Fatal(err)
	}
	if got != server.URL+"/signed/audio.mp3?secret=redacted" {
		t.Fatalf("URL = %q", got)
	}
}

func TestAuthorizedMediaURLRejectsForbiddenWithoutLeakingToken(t *testing.T) {
	t.Parallel()
	const token = "must-never-appear"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
	}))
	defer server.Close()
	client := newAPIClient()
	client.baseURL = server.URL
	_, err := client.authorizedMediaURL(context.Background(), "123", token)
	if err == nil {
		t.Fatal("expected error")
	}
	if contains := stringContains(err.Error(), token); contains {
		t.Fatalf("错误泄露令牌: %v", err)
	}
}

func TestAPIClientPersistsServerSessionCookies(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/bootstrap":
			http.SetCookie(w, &http.Cookie{Name: "acw_tc", Value: "session-value", Path: "/"})
			fmt.Fprint(w, `{}`)
		case "/gapi/v1/medias/protected/radios/123":
			cookie, err := r.Cookie("acw_tc")
			if err != nil || cookie.Value != "session-value" {
				t.Fatalf("session cookie = %#v, err=%v", cookie, err)
			}
			fmt.Fprintf(w, `{"url":%q}`, serverURL(r)+"/audio.mp3")
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	client := newAPIClient()
	client.baseURL = server.URL
	var bootstrap map[string]any
	if err := client.getJSON(context.Background(), server.URL+"/bootstrap", "", &bootstrap); err != nil {
		t.Fatal(err)
	}
	if _, err := client.authorizedMediaURL(context.Background(), "123", "token"); err != nil {
		t.Fatal(err)
	}
}

func serverURL(r *http.Request) string {
	return "http://" + r.Host
}

func stringContains(value, substring string) bool {
	for i := 0; i+len(substring) <= len(value); i++ {
		if value[i:i+len(substring)] == substring {
			return true
		}
	}
	return false
}
