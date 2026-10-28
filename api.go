package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"path"
	"regexp"
	"strings"
	"time"
)

const (
	defaultBaseURL  = "https://www.gcores.com"
	defaultAudioURL = "https://alioss.gcores.com/uploads/audio"
	defaultImageURL = "https://image.gcores.com"
	userAgent       = "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/141.0.0.0 Safari/537.36"
)

var numericID = regexp.MustCompile(`^[0-9]+$`)

type apiClient struct {
	baseURL  string
	audioURL string
	imageURL string
	http     *http.Client
}

func newAPIClient() *apiClient {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.ResponseHeaderTimeout = 30 * time.Second
	transport.MaxIdleConnsPerHost = 4
	jar, _ := cookiejar.New(nil)
	return &apiClient{
		baseURL:  defaultBaseURL,
		audioURL: defaultAudioURL,
		imageURL: defaultImageURL,
		http:     &http.Client{Transport: transport, Jar: jar},
	}
}

func (c *apiClient) fetchAlbum(ctx context.Context, id string) (*album, error) {
	if !numericID.MatchString(id) {
		return nil, fmt.Errorf("专辑 ID 只能包含数字")
	}
	endpoint := fmt.Sprintf("%s/gapi/v1/albums/%s?include=published-audiobooks%%2Cpublished-audiobooks.media", c.baseURL, id)
	var response entityResponse
	if err := c.getJSON(ctx, endpoint, "", &response); err != nil {
		return nil, fmt.Errorf("获取专辑信息失败: %w", err)
	}
	if response.Data.ID == "" || response.Data.Type != "albums" {
		return nil, errors.New("专辑接口返回了不完整的数据")
	}

	entities := indexEntities(response.Included)
	result := &album{
		ID:     response.Data.ID,
		Title:  response.Data.Attributes.Title,
		Author: response.Data.Attributes.Author,
		Cover:  response.Data.Attributes.Cover,
	}
	for _, ref := range response.Data.Relationships.PublishedAudiobooks.Data {
		radio, ok := entities[entityKey(ref.Type, ref.ID)]
		if !ok {
			var err error
			radio, entities, err = c.fetchRadio(ctx, ref.ID, entities)
			if err != nil {
				return nil, fmt.Errorf("章节 %s 数据缺失: %w", ref.ID, err)
			}
		}
		mediaRef := radio.Relationships.Media.Data
		mediaEntity, ok := entities[entityKey(mediaRef.Type, mediaRef.ID)]
		if !ok {
			var err error
			radio, entities, err = c.fetchRadio(ctx, ref.ID, entities)
			if err != nil {
				return nil, fmt.Errorf("章节 %s 的媒体数据缺失: %w", ref.ID, err)
			}
			mediaRef = radio.Relationships.Media.Data
			mediaEntity, ok = entities[entityKey(mediaRef.Type, mediaRef.ID)]
		}
		if !ok || mediaEntity.ID == "" {
			return nil, fmt.Errorf("章节 %s 没有可用媒体", ref.ID)
		}
		result.Chapters = append(result.Chapters, chapter{
			ID:        radio.ID,
			Title:     radio.Attributes.Title,
			IsFree:    radio.Attributes.IsFree,
			VendorSrc: radio.Attributes.VendorSrc,
			Media: media{
				ID:           mediaEntity.ID,
				Audio:        mediaEntity.Attributes.Audio,
				MediaType:    mediaEntity.Attributes.MediaType,
				OriginalSrc:  mediaEntity.Attributes.OriginalSrc,
				PlaybackType: mediaEntity.Attributes.PlaybackType,
			},
		})
	}
	if len(result.Chapters) == 0 {
		return nil, errors.New("专辑没有已发布的有声书章节")
	}
	return result, nil
}

func (c *apiClient) fetchRadio(ctx context.Context, id string, existing map[string]entity) (entity, map[string]entity, error) {
	if !numericID.MatchString(id) {
		return entity{}, existing, errors.New("无效章节 ID")
	}
	endpoint := fmt.Sprintf("%s/gapi/v1/radios/%s?include=media", c.baseURL, id)
	var response entityResponse
	if err := c.getJSON(ctx, endpoint, "", &response); err != nil {
		return entity{}, existing, err
	}
	for key, value := range indexEntities(response.Included) {
		existing[key] = value
	}
	return response.Data, existing, nil
}

func indexEntities(items []entity) map[string]entity {
	result := make(map[string]entity, len(items))
	for _, item := range items {
		result[entityKey(item.Type, item.ID)] = item
	}
	return result
}

func entityKey(entityType, id string) string { return entityType + "#" + id }

func (c *apiClient) authorizedMediaURL(ctx context.Context, radioID, token string) (string, error) {
	if token == "" {
		return "", errors.New("尚未登录，请先运行 gcoredl login")
	}
	endpoint := fmt.Sprintf("%s/gapi/v1/medias/protected/radios/%s?format=json", c.baseURL, radioID)
	var response struct {
		URL  string `json:"url"`
		Data struct {
			URL        string `json:"url"`
			Attributes struct {
				URL string `json:"url"`
			} `json:"attributes"`
		} `json:"data"`
	}
	if err := c.getJSON(ctx, endpoint, token, &response); err != nil {
		return "", fmt.Errorf("获取章节授权地址失败: %w", err)
	}
	mediaURL := response.URL
	if mediaURL == "" {
		mediaURL = response.Data.URL
	}
	if mediaURL == "" {
		mediaURL = response.Data.Attributes.URL
	}
	if err := validateRemoteURL(mediaURL); err != nil {
		return "", fmt.Errorf("授权接口没有返回有效音频地址: %w", err)
	}
	return mediaURL, nil
}

func (c *apiClient) directMediaURL(ch chapter) (string, error) {
	if ch.VendorSrc != "" {
		if err := validateRemoteURL(ch.VendorSrc); err == nil {
			return ch.VendorSrc, nil
		}
	}
	if ch.Media.OriginalSrc != "" {
		if err := validateRemoteURL(ch.Media.OriginalSrc); err == nil {
			return ch.Media.OriginalSrc, nil
		}
	}
	if ch.Media.Audio == "" {
		return "", errors.New("媒体缺少 audio 字段")
	}
	if parsed, err := url.Parse(ch.Media.Audio); err == nil && parsed.IsAbs() {
		if err := validateRemoteURL(ch.Media.Audio); err != nil {
			return "", err
		}
		return ch.Media.Audio, nil
	}
	return strings.TrimRight(c.audioURL, "/") + "/" + url.PathEscape(path.Base(ch.Media.Audio)), nil
}

func (c *apiClient) coverURL(cover string) (string, error) {
	if cover == "" {
		return "", errors.New("专辑缺少封面字段")
	}
	if parsed, err := url.Parse(cover); err == nil && parsed.IsAbs() {
		if err := validateRemoteURL(cover); err != nil {
			return "", err
		}
		return cover, nil
	}
	return strings.TrimRight(c.imageURL, "/") + "/" + url.PathEscape(path.Base(cover)), nil
}

func validateRemoteURL(raw string) error {
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Host == "" || (parsed.Scheme != "https" && parsed.Scheme != "http") {
		return errors.New("地址不是有效的 HTTP(S) URL")
	}
	if parsed.User != nil {
		return errors.New("地址不能包含用户名或密码")
	}
	return nil
}

func (c *apiClient) getJSON(ctx context.Context, endpoint, token string, target any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/vnd.api+json, application/json")
	req.Header.Set("Content-Type", "application/vnd.api+json")
	req.Header.Set("User-Agent", userAgent)
	req.Header.Set("Referer", c.baseURL+"/")
	if token != "" {
		req.Header.Set("Authorization", "Token token="+token)
		// 浏览器的同源 fetch 会同时携带 appToken Cookie；部分旧中间件仍会读取 Cookie。
		// 这里只对 www.gcores.com API 请求添加，下载短时 URL 时绝不携带登录令牌。
		req.AddCookie(&http.Cookie{Name: "appToken", Value: token, Path: "/", Secure: true})
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return safeNetworkError(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return statusError(resp.StatusCode)
	}
	decoder := json.NewDecoder(io.LimitReader(resp.Body, 16<<20))
	if err := decoder.Decode(target); err != nil {
		return fmt.Errorf("JSON 解析失败: %w", err)
	}
	return nil
}

type httpStatusError struct {
	Status int
}

func (e *httpStatusError) Error() string {
	switch e.Status {
	case http.StatusUnauthorized:
		return "HTTP 401（登录已失效，请重新运行 gcoredl login）"
	case http.StatusForbidden:
		return "HTTP 403（账号未获准访问、尚未购买，或登录已失效）"
	case http.StatusNotFound:
		return "HTTP 404（资源不存在）"
	default:
		return fmt.Sprintf("HTTP %d", e.Status)
	}
}

func (e *httpStatusError) retryable() bool {
	return e.Status == http.StatusTooManyRequests || e.Status >= 500
}

func statusError(status int) error { return &httpStatusError{Status: status} }

func safeNetworkError(err error) error {
	var urlErr *url.Error
	if errors.As(err, &urlErr) {
		return urlErr.Err
	}
	return err
}
