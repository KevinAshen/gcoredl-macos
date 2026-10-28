package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

type downloader struct {
	api     *apiClient
	creds   credentialStore
	out     io.Writer
	err     io.Writer
	retries int
	ffmpeg  string
	sleep   func(context.Context, time.Duration) error
}

type downloadResult struct {
	downloaded int
	skipped    int
	failed     []chapterFailure
}

type chapterFailure struct {
	title string
	err   error
}

func (d *downloader) downloadAlbum(ctx context.Context, book *album, output string) downloadResult {
	result := downloadResult{}
	cover, mimeType, err := d.downloadCover(ctx, book, output)
	if err != nil {
		result.failed = append(result.failed, chapterFailure{title: "专辑封面", err: err})
		return result
	}

	var token string
	for i, chapter := range book.Chapters {
		if err := ctx.Err(); err != nil {
			result.failed = append(result.failed, chapterFailure{title: chapter.Title, err: err})
			break
		}
		target := filepath.Join(output, fmt.Sprintf("%03d - %s.mp3", i+1, sanitizeFilename(chapter.Title)))
		exists, statErr := regularFileExists(target)
		if statErr != nil {
			result.failed = append(result.failed, chapterFailure{title: chapter.Title, err: statErr})
			continue
		}
		if exists {
			fmt.Fprintf(d.out, "[%d/%d] 跳过（已存在）：%s\n", i+1, len(book.Chapters), filepath.Base(target))
			result.skipped++
			continue
		}

		fmt.Fprintf(d.out, "[%d/%d] 下载：%s\n", i+1, len(book.Chapters), chapter.Title)
		if chapter.Media.MediaType == "protected_audio" && token == "" {
			token, err = d.creds.Load()
			if err != nil {
				result.failed = append(result.failed, chapterFailure{title: chapter.Title, err: err})
				continue
			}
		}
		err = d.downloadChapter(ctx, book, chapter, i+1, len(book.Chapters), target, cover, mimeType, token)
		if err != nil {
			result.failed = append(result.failed, chapterFailure{title: chapter.Title, err: err})
			continue
		}
		result.downloaded++
	}
	return result
}

func (d *downloader) downloadCover(ctx context.Context, book *album, output string) ([]byte, string, error) {
	coverURL, err := d.api.coverURL(book.Cover)
	if err != nil {
		return nil, "", err
	}
	ext := strings.ToLower(filepath.Ext(strings.Split(book.Cover, "?")[0]))
	if ext != ".png" && ext != ".webp" {
		ext = ".jpg"
	}
	coverPath := filepath.Join(output, "cover"+ext)
	exists, err := regularFileExists(coverPath)
	if err != nil {
		return nil, "", err
	}
	if !exists {
		fmt.Fprintln(d.out, "下载专辑封面……")
		if err := d.downloadFile(ctx, coverURL, coverPath+".part", nil); err != nil {
			_ = os.Remove(coverPath + ".part")
			return nil, "", fmt.Errorf("下载封面失败: %w", err)
		}
		if err := os.Rename(coverPath+".part", coverPath); err != nil {
			return nil, "", fmt.Errorf("保存封面失败: %w", err)
		}
	}
	data, err := os.ReadFile(coverPath)
	if err != nil {
		return nil, "", fmt.Errorf("读取封面失败: %w", err)
	}
	if len(data) == 0 {
		return nil, "", errors.New("封面文件为空")
	}
	mimeType := http.DetectContentType(data)
	if !strings.HasPrefix(mimeType, "image/") {
		return nil, "", fmt.Errorf("封面格式无效（%s）", mimeType)
	}
	return data, mimeType, nil
}

func (d *downloader) downloadChapter(ctx context.Context, book *album, chapter chapter, track, total int, target string, cover []byte, coverMIME, token string) error {
	var lastErr error
	for attempt := 1; attempt <= d.retries; attempt++ {
		if attempt > 1 {
			fmt.Fprintf(d.out, "  重试 %d/%d……\n", attempt, d.retries)
			if err := d.wait(ctx, time.Duration(attempt-1)*time.Second); err != nil {
				return err
			}
		}

		mediaURL, err := d.resolveMediaURL(ctx, chapter, token)
		if err != nil {
			lastErr = err
			if !isRetryable(err) {
				break
			}
			continue
		}

		part := target + ".part"
		sourcePart := target + ".source.part"
		_ = os.Remove(part)
		_ = os.Remove(sourcePart)
		inputPath := part
		if !isMP3Source(chapter.Media.Audio, mediaURL) {
			inputPath = sourcePart
		}
		if err = d.downloadFile(ctx, mediaURL, inputPath, d.progress(filepath.Base(target))); err != nil {
			fmt.Fprintln(d.out)
			lastErr = err
			_ = os.Remove(part)
			_ = os.Remove(sourcePart)
			if !isRetryable(err) {
				break
			}
			continue
		}
		fmt.Fprintln(d.out)
		if inputPath == sourcePart {
			if err = d.convertToMP3(ctx, sourcePart, part); err != nil {
				lastErr = err
				_ = os.Remove(part)
				_ = os.Remove(sourcePart)
				break
			}
			_ = os.Remove(sourcePart)
		}
		if err = writeID3(part, tagData{
			Title:      chapter.Title,
			Album:      book.Title,
			Artist:     book.Author,
			Track:      track,
			TrackTotal: total,
			Cover:      cover,
			CoverMIME:  coverMIME,
		}); err != nil {
			lastErr = fmt.Errorf("写入 ID3 标签失败: %w", err)
			_ = os.Remove(part)
			break
		}
		if err = os.Rename(part, target); err != nil {
			lastErr = fmt.Errorf("保存 MP3 失败: %w", err)
			_ = os.Remove(part)
			break
		}
		fmt.Fprintf(d.out, "  已保存：%s\n", filepath.Base(target))
		return nil
	}
	if lastErr == nil {
		lastErr = errors.New("未知下载错误")
	}
	return lastErr
}

func (d *downloader) resolveMediaURL(ctx context.Context, chapter chapter, token string) (string, error) {
	switch chapter.Media.MediaType {
	case "protected_audio":
		return d.api.authorizedMediaURL(ctx, chapter.ID, token)
	case "audio":
		return d.api.directMediaURL(chapter)
	default:
		return "", fmt.Errorf("不支持的媒体类型 %q；不会尝试绕过 DRM 或平台限制", chapter.Media.MediaType)
	}
}

func (d *downloader) downloadFile(ctx context.Context, sourceURL, target string, progress func(int64, int64)) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, sourceURL, nil)
	if err != nil {
		return errors.New("无法创建下载请求")
	}
	req.Header.Set("User-Agent", userAgent)
	req.Header.Set("Referer", d.api.baseURL+"/")
	resp, err := d.api.http.Do(req)
	if err != nil {
		return safeNetworkError(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return statusError(resp.StatusCode)
	}
	file, err := os.OpenFile(target, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
	if err != nil {
		return fmt.Errorf("创建临时文件失败: %w", err)
	}
	written, copyErr := copyWithProgress(file, resp.Body, resp.ContentLength, progress)
	closeErr := file.Close()
	if copyErr != nil {
		return fmt.Errorf("接收音频失败: %w", copyErr)
	}
	if closeErr != nil {
		return fmt.Errorf("写入音频失败: %w", closeErr)
	}
	if written == 0 {
		return errors.New("服务器返回了空文件")
	}
	return nil
}

func copyWithProgress(dst io.Writer, src io.Reader, total int64, callback func(int64, int64)) (int64, error) {
	buffer := make([]byte, 128*1024)
	var written int64
	lastUpdate := time.Now()
	for {
		count, readErr := src.Read(buffer)
		if count > 0 {
			n, writeErr := dst.Write(buffer[:count])
			written += int64(n)
			if writeErr != nil {
				return written, writeErr
			}
			if n != count {
				return written, io.ErrShortWrite
			}
			if callback != nil && (time.Since(lastUpdate) >= time.Second || readErr == io.EOF) {
				callback(written, total)
				lastUpdate = time.Now()
			}
		}
		if readErr == io.EOF {
			if callback != nil {
				callback(written, total)
			}
			return written, nil
		}
		if readErr != nil {
			return written, readErr
		}
	}
}

func (d *downloader) progress(_ string) func(int64, int64) {
	return func(done, total int64) {
		if total > 0 {
			fmt.Fprintf(d.out, "  进度：%5.1f%% (%s / %s)\r", float64(done)*100/float64(total), humanBytes(done), humanBytes(total))
		} else {
			fmt.Fprintf(d.out, "  已下载：%s\r", humanBytes(done))
		}
	}
}

func humanBytes(value int64) string {
	const unit = 1024
	if value < unit {
		return fmt.Sprintf("%d B", value)
	}
	div, exp := int64(unit), 0
	for n := value / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %ciB", float64(value)/float64(div), "KMGTPE"[exp])
}

func (d *downloader) convertToMP3(ctx context.Context, source, target string) error {
	ffmpegPath, err := exec.LookPath(d.ffmpeg)
	if err != nil {
		return errors.New("音频不是 MP3，且未找到 ffmpeg；请先运行 brew install ffmpeg，或用 -ffmpeg 指定路径")
	}
	cmd := exec.CommandContext(ctx, ffmpegPath,
		"-nostdin", "-hide_banner", "-loglevel", "error", "-y",
		"-i", source, "-map_metadata", "-1", "-vn", "-codec:a", "libmp3lame", "-q:a", "2", target)
	output, err := cmd.CombinedOutput()
	if err != nil {
		message := strings.TrimSpace(string(output))
		if len(message) > 500 {
			message = message[:500] + "…"
		}
		if message == "" {
			message = err.Error()
		}
		return fmt.Errorf("ffmpeg 转换失败: %s", message)
	}
	return nil
}

func (d *downloader) wait(ctx context.Context, duration time.Duration) error {
	if d.sleep != nil {
		return d.sleep(ctx, duration)
	}
	timer := time.NewTimer(duration)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func isRetryable(err error) bool {
	var status *httpStatusError
	if errors.As(err, &status) {
		return status.retryable()
	}
	return !errors.Is(err, context.Canceled) && !strings.Contains(err.Error(), "不支持的媒体类型") &&
		!strings.Contains(err.Error(), "尚未登录") && !strings.Contains(err.Error(), "钥匙串")
}

func isMP3Source(audioName, sourceURL string) bool {
	return strings.EqualFold(filepath.Ext(strings.Split(audioName, "?")[0]), ".mp3") ||
		strings.EqualFold(filepath.Ext(strings.Split(sourceURL, "?")[0]), ".mp3")
}

func regularFileExists(name string) (bool, error) {
	info, err := os.Stat(name)
	if err == nil {
		if !info.Mode().IsRegular() {
			return false, fmt.Errorf("目标路径不是普通文件: %s", name)
		}
		return true, nil
	}
	if os.IsNotExist(err) {
		return false, nil
	}
	return false, err
}

var invalidFilenameChars = regexp.MustCompile(`[<>:"/\\|?*\x00-\x1f]`)

func sanitizeFilename(name string) string {
	name = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return '_'
		}
		return r
	}, name)
	name = invalidFilenameChars.ReplaceAllString(name, "_")
	name = strings.Join(strings.Fields(name), " ")
	name = strings.Trim(name, ". ")
	if name == "" || strings.Trim(name, "_") == "" {
		name = "未命名章节"
	}
	const maxBytes = 180
	if len(name) > maxBytes {
		name = name[:maxBytes]
		for !utf8.ValidString(name) {
			name = name[:len(name)-1]
		}
		name = strings.TrimRight(name, ". ")
	}
	return name
}
