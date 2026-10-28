# gcoredl

macOS 命令行工具：批量保存当前账号已购买、且机核服务端允许播放/下载的有声书音频，整理为带封面和完整 ID3 标签的 MP3。

本分支基于原始 [gcoredl](https://github.com/yankeguo-deprecated/gcoredl) 修改，适配机核当前网页的 API 和认证方式。它不会绕过 DRM、购买权限或服务端限制。

## 功能

- 输入专辑 ID，按 `published-audiobooks` 的原始顺序获取完整章节
- 支持当前 `audio` 和 `protected_audio` 媒体类型
- `protected_audio` 使用网页当前的官方授权流程：请求短时有效的获准播放地址
- 失败自动重试；每次重试都会重新请求短时授权地址
- 使用 `.part` 临时文件，完成后原子改名；已存在的 MP3 自动跳过
- 输出下载进度和最终成功/跳过/失败汇总
- 文件名自动清理，使用 `001 - 章节名.mp3` 格式
- 写入标题、专辑、作者、曲目号/总曲目数、流派和封面 ID3 标签
- 对官方授权返回的无 DRM、非 MP3 音频，可调用 ffmpeg 转换为 MP3
- 登录令牌由 macOS 钥匙串保存，不出现在命令历史和 gcoredl 日志中

## 系统要求

- macOS 13 或更高版本；发布脚本同时支持 Intel (`amd64`) 和 Apple Silicon (`arm64`)
- 自行编译需要 Go 1.22 或更高版本
- 只有源音频不是 MP3 时才需要 ffmpeg：

  ```bash
  brew install ffmpeg
  ```

机核当前有声书响应中的 `audio` 通常已经是 MP3，因此多数下载不需要 ffmpeg。

## 下载

从 [Releases](https://github.com/KevinAshen/gcoredl-macos/releases/latest) 下载与你的 Mac 对应的文件：

- Intel：`gcoredl-darwin-amd64`
- Apple Silicon（M1/M2/M3/M4）：`gcoredl-darwin-arm64`

下载后赋予执行权限并查看版本：

```bash
chmod +x ./gcoredl-darwin-arm64
./gcoredl-darwin-arm64 version
```

发布文件会同时提供 `SHA256SUMS`。当前二进制未使用 Apple Developer ID 签名；如果 macOS 阻止运行，可选择按照下一节从源码自行编译。

## 编译

克隆并在仓库目录执行：

```bash
git clone https://github.com/KevinAshen/gcoredl-macos.git
cd gcoredl-macos
go test ./...
go build -trimpath -o gcoredl .
```

构建 macOS Intel 和 Apple Silicon 发行文件：

```bash
VERSION=2.1.0 ./dist.sh
```

输出位于：

- `dist/gcoredl-darwin-amd64`（Intel Mac）
- `dist/gcoredl-darwin-arm64`（Apple Silicon）

## 登录

下载受保护章节时需要登录。登录凭证仅在本机处理，并保存在 macOS 钥匙串中。

1. 使用 Chrome 登录 [机核网](https://www.gcores.com/)。
2. 打开 Chrome 开发者工具。
3. 进入 **Console**，执行下面这行。它只会把当前页面的 `appToken` 解码后复制到本机剪贴板，不会在控制台显示令牌：

   ```javascript
   copy(decodeURIComponent(document.cookie.split("; ").find(v => v.startsWith("appToken=")).slice("appToken=".length)))
   ```

4. 执行：

   ```bash
   ./gcoredl login
   ```

5. 在两个隐藏提示中各粘贴一次并回车。输入不回显是正常现象。

macOS 首次保存或读取凭证时可能显示钥匙串访问确认。

登录后可以先检查账号是否有权访问专辑中的受保护章节：

```bash
./gcoredl auth-check -album 222
```

退出并移除凭证：

```bash
./gcoredl logout
```

## 使用

先查看专辑元数据和章节顺序，不下载：

```bash
./gcoredl inspect -album 64
```

下载到默认目录 `output-64`：

```bash
./gcoredl download -album 64
```

指定目录、重试次数或 ffmpeg 路径：

```bash
./gcoredl download \
  -album 64 \
  -output "$HOME/Music/有声书/克苏鲁神话III" \
  -retries 5 \
  -ffmpeg /usr/local/bin/ffmpeg
```

兼容原项目的参数形式，但不再接受不安全的 `-auth` 参数：

```bash
./gcoredl -album 64
```

下载中断后直接重跑同一命令即可。已经存在的最终 `.mp3` 文件会跳过，未完成的 `.part` 文件会在重试时替换。

## 当前 API 兼容说明

截至 2026-10-28，公开响应和网页前端显示：

- 专辑：`GET /gapi/v1/albums/{id}?include=published-audiobooks,published-audiobooks.media`
- 节目回退查询：`GET /gapi/v1/radios/{id}?include=media`
- 免费音频：`https://alioss.gcores.com/uploads/audio/{audio}`
- 受保护音频授权：`GET /gapi/v1/medias/protected/radios/{id}?format=json`
- 当前登录头：`Authorization: Token token=<appToken>`

旧项目直接携带 `auth_token` Cookie 访问受保护地址、并跟随重定向；当前接口对这种直接访问返回 403。网页现在先请求 JSON 授权结果，再播放其中的短时 URL，本分支遵循同一流程。

## 权限和限制

- 工具只会使用机核服务端为当前账号返回的授权地址。
- 401/403 会明确报错，不会尝试伪造权限、绕过 DRM 或猜测资源地址。
- 未购买、已下架、平台不允许导出、令牌过期或存在 DRM 的内容不会被强行下载/转换。
- 机核 API 不是公开稳定 API；平台未来调整接口时，工具可能需要再次更新。
- 请只备份本人合法购买且平台允许访问的内容，并遵守机核条款和版权规定。

## 测试

```bash
go test -race ./...
go vet ./...
```

测试覆盖章节顺序、缺失 `included` 时的节目接口回退、当前授权头和 `format=json`、403 错误脱敏、重试、原子保存、已存在跳过、文件名清理、ID3 标签和 ffmpeg 调用。

## License

MIT。原作者 Guo Y.K.，本分支保留原项目许可证。
