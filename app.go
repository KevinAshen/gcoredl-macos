package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

type app struct {
	out   io.Writer
	err   io.Writer
	api   *apiClient
	creds credentialStore
}

func newApp(out, errOut io.Writer) *app {
	return &app{out: out, err: errOut, api: newAPIClient(), creds: macOSKeychain{}}
}

func (a *app) run(ctx context.Context, args []string) error {
	if len(args) == 0 {
		a.printUsage()
		return errors.New("缺少命令")
	}
	switch args[0] {
	case "login":
		return a.login()
	case "logout":
		if err := a.creds.Delete(); err != nil {
			return err
		}
		fmt.Fprintln(a.out, "已从 macOS 钥匙串移除机核登录令牌。")
		return nil
	case "download":
		return a.download(ctx, args[1:])
	case "inspect":
		return a.inspect(ctx, args[1:])
	case "auth-check":
		return a.authCheck(ctx, args[1:])
	case "version", "--version", "-version":
		fmt.Fprintf(a.out, "gcoredl %s\n", version)
		return nil
	case "help", "--help", "-h":
		a.printUsage()
		return nil
	default:
		// 兼容原项目的 `gcoredl -album 64`，也允许 `gcoredl 64`。
		if strings.HasPrefix(args[0], "-") {
			return a.download(ctx, args)
		}
		if numericID.MatchString(args[0]) {
			return a.download(ctx, append([]string{"-album", args[0]}, args[1:]...))
		}
		a.printUsage()
		return fmt.Errorf("未知命令 %q", args[0])
	}
}

func (a *app) login() error {
	fmt.Fprintln(a.out, "请先在 Chrome 中登录 https://www.gcores.com/")
	fmt.Fprintln(a.out, "然后在机核页面的开发者工具 Console 中执行：")
	fmt.Fprintln(a.out, `copy(decodeURIComponent(document.cookie.split("; ").find(v => v.startsWith("appToken=")).slice("appToken=".length)))`)
	fmt.Fprintln(a.out, "接下来在 gcoredl 的隐藏提示中粘贴两次；输入不会回显，也不会进入命令历史或日志。")
	if err := a.creds.SaveInteractively(); err != nil {
		return err
	}
	token, err := a.creds.Load()
	if err != nil {
		return err
	}
	fmt.Fprintf(a.out, "登录令牌已完整保存到 macOS 钥匙串（%d 字符，回读校验通过）。\n", len(token))
	return nil
}

type downloadOptions struct {
	albumID string
	output  string
	retries int
	ffmpeg  string
}

func parseDownloadOptions(args []string) (downloadOptions, error) {
	var options downloadOptions
	flags := flag.NewFlagSet("download", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	flags.StringVar(&options.albumID, "album", "", "专辑 ID")
	flags.StringVar(&options.output, "output", "", "输出目录")
	flags.IntVar(&options.retries, "retries", 3, "单章最大尝试次数")
	flags.StringVar(&options.ffmpeg, "ffmpeg", "ffmpeg", "ffmpeg 路径")
	if err := flags.Parse(args); err != nil {
		return options, fmt.Errorf("参数错误: %w", err)
	}
	if options.albumID == "" && flags.NArg() == 1 {
		options.albumID = flags.Arg(0)
	}
	if !numericID.MatchString(options.albumID) {
		return options, errors.New("必须用 -album 指定纯数字专辑 ID，例如：gcoredl download -album 64")
	}
	if options.retries < 1 || options.retries > 10 {
		return options, errors.New("-retries 必须在 1 到 10 之间")
	}
	if options.output == "" {
		options.output = "output-" + options.albumID
	}
	return options, nil
}

func (a *app) download(ctx context.Context, args []string) error {
	options, err := parseDownloadOptions(args)
	if err != nil {
		return err
	}
	book, err := a.api.fetchAlbum(ctx, options.albumID)
	if err != nil {
		return err
	}
	absOutput, err := filepath.Abs(options.output)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(absOutput, 0o755); err != nil {
		return fmt.Errorf("创建输出目录失败: %w", err)
	}
	fmt.Fprintf(a.out, "专辑：%s\n作者：%s\n章节：%d\n目录：%s\n\n", book.Title, book.Author, len(book.Chapters), absOutput)

	d := downloader{
		api:     a.api,
		creds:   a.creds,
		out:     a.out,
		err:     a.err,
		retries: options.retries,
		ffmpeg:  options.ffmpeg,
	}
	result := d.downloadAlbum(ctx, book, absOutput)
	fmt.Fprintf(a.out, "\n完成：成功 %d，跳过 %d，失败 %d。\n", result.downloaded, result.skipped, len(result.failed))
	if len(result.failed) > 0 {
		fmt.Fprintln(a.err, "失败章节：")
		for _, failure := range result.failed {
			fmt.Fprintf(a.err, "  - %s：%v\n", failure.title, failure.err)
		}
		return errors.New("部分章节下载失败；可直接重跑，已完成文件会自动跳过")
	}
	return nil
}

func (a *app) inspect(ctx context.Context, args []string) error {
	options, err := parseDownloadOptions(args)
	if err != nil {
		return err
	}
	book, err := a.api.fetchAlbum(ctx, options.albumID)
	if err != nil {
		return err
	}
	fmt.Fprintf(a.out, "专辑 %s（%s），共 %d 章：\n", book.Title, book.Author, len(book.Chapters))
	for i, chapter := range book.Chapters {
		fmt.Fprintf(a.out, "%s  %s  [%s]\n", leftPad(i+1, len(book.Chapters)), chapter.Title, chapter.Media.MediaType)
	}
	return nil
}

func (a *app) authCheck(ctx context.Context, args []string) error {
	options, err := parseDownloadOptions(args)
	if err != nil {
		return err
	}
	book, err := a.api.fetchAlbum(ctx, options.albumID)
	if err != nil {
		return err
	}
	var protected *chapter
	for i := range book.Chapters {
		if book.Chapters[i].Media.MediaType == "protected_audio" {
			protected = &book.Chapters[i]
			break
		}
	}
	if protected == nil {
		fmt.Fprintln(a.out, "该专辑没有需要账号授权的 protected_audio 章节。")
		return nil
	}
	token, err := a.creds.Load()
	if err != nil {
		return err
	}
	if _, err := a.api.authorizedMediaURL(ctx, protected.ID, token); err != nil {
		return err
	}
	fmt.Fprintf(a.out, "授权验证成功：%s\n", protected.Title)
	fmt.Fprintln(a.out, "机核已为当前账号返回获准播放地址，可以开始下载。")
	return nil
}

func leftPad(value, total int) string {
	width := len(strconv.Itoa(total))
	return fmt.Sprintf("%0*d", width, value)
}

func (a *app) printUsage() {
	fmt.Fprintln(a.out, `gcoredl - 机核已购有声书下载工具（macOS）

用法：
  gcoredl login
  gcoredl inspect -album 64
  gcoredl auth-check -album 64
  gcoredl download -album 64 [-output 目录] [-retries 3]
  gcoredl logout

兼容旧用法：gcoredl -album 64

凭证保存在 macOS 钥匙串。工具只请求账号已获准播放的地址，不绕过购买权限或 DRM。`)
}
