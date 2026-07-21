package qqbot

import (
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"html"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"diana-qq-bot/model/netguard"
)

const (
	defaultPlatformTimeout  = 60 * time.Second
	defaultVideoMaxMB       = 100
	defaultVideoMaxDuration = 480
	douyinVideoAPI          = "https://www.douyin.com/aweme/v1/web/aweme/detail/?device_platform=webapp&aid=6383&channel=channel_pc_web&aweme_id=%s&pc_client_type=1&version_code=190500&version_name=19.5.0&cookie_enabled=true&screen_width=1344&screen_height=756&browser_language=zh-CN&browser_platform=Win32&browser_name=Firefox&browser_version=118.0&browser_online=true&engine_name=Gecko&engine_version=109.0&os_name=Windows&os_version=10&cpu_core_num=16&device_memory=&platform=PC"
	douyinPlayURL           = "https://aweme.snssdk.com/aweme/v1/play/?video_id=%s&ratio=1080p&line=0"
	xiaohongshuExploreURL   = "https://www.xiaohongshu.com/explore/%s?xsec_source=%s&xsec_token=%s"
	resolverUserAgent       = "Mozilla/5.0 (Windows NT 10.0; WOW64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/55.0.2883.87 UBrowser/6.2.4098.3 Safari/537.36"
)

//go:embed resolver_assets/a-bogus.js
var douyinABogusJS string

var (
	douyinIDPattern       = regexp.MustCompile(`(?i)/(?:video|note)/([0-9]+)`)
	xiaohongshuStateRegex = regexp.MustCompile(`(?is)window\.__INITIAL_STATE__=(.*?)</script>`)
)

type ytdlpInfo struct {
	Title            string            `json:"title"`
	Duration         float64           `json:"duration"`
	RequestedFormats []ytdlpFormat     `json:"requested_formats"`
	Formats          []ytdlpFormat     `json:"formats"`
	HTTPHeaders      map[string]string `json:"http_headers"`
}

type ytdlpFormat struct {
	URL         string            `json:"url"`
	Ext         string            `json:"ext"`
	VCodec      string            `json:"vcodec"`
	ACodec      string            `json:"acodec"`
	Height      int               `json:"height"`
	Width       int               `json:"width"`
	Filesize    int64             `json:"filesize"`
	ApproxSize  int64             `json:"filesize_approx"`
	HTTPHeaders map[string]string `json:"http_headers"`
}

type bilibiliViewResponse struct {
	Code int `json:"code"`
	Data struct {
		BVID     string `json:"bvid"`
		CID      int64  `json:"cid"`
		Title    string `json:"title"`
		Pic      string `json:"pic"`
		Desc     string `json:"desc"`
		Duration int    `json:"duration"`
		Owner    struct {
			Name string `json:"name"`
		} `json:"owner"`
		Stat struct {
			View     int `json:"view"`
			Danmaku  int `json:"danmaku"`
			Reply    int `json:"reply"`
			Favorite int `json:"favorite"`
			Coin     int `json:"coin"`
			Share    int `json:"share"`
			Like     int `json:"like"`
		} `json:"stat"`
		Pages []struct {
			CID      int64 `json:"cid"`
			Duration int   `json:"duration"`
		} `json:"pages"`
	} `json:"data"`
	Message string `json:"message"`
}

type bilibiliPlayURLResponse struct {
	Code int `json:"code"`
	Data struct {
		Dash struct {
			Video []bilibiliDashMedia `json:"video"`
			Audio []bilibiliDashMedia `json:"audio"`
		} `json:"dash"`
		DURL []struct {
			URL       string   `json:"url"`
			BackupURL []string `json:"backup_url"`
			Size      int64    `json:"size"`
		} `json:"durl"`
	} `json:"data"`
	Message string `json:"message"`
}

type bilibiliDashMedia struct {
	BaseURL    string   `json:"base_url"`
	BaseURL2   string   `json:"baseUrl"`
	BackupURL  []string `json:"backup_url"`
	BackupURL2 []string `json:"backupUrl"`
	ID         int      `json:"id"`
	Width      int      `json:"width"`
	Height     int      `json:"height"`
	Bandwidth  int      `json:"bandwidth"`
	Codecs     string   `json:"codecs"`
}

func downloadPlatformVideoFile(ctx context.Context, raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	if !isKnownResolverPlatformURL(raw) {
		return ""
	}
	if isBilibiliURL(raw) {
		if path := downloadBilibiliVideoFile(ctx, raw); path != "" {
			return path
		}
	}
	if isDouyinURL(raw) {
		if path := downloadDouyinVideoFile(ctx, raw); path != "" {
			return path
		}
	}
	if isXiaohongshuURL(raw) {
		if path := downloadXiaohongshuVideoFile(ctx, raw); path != "" {
			return path
		}
	}
	if isTwitterURL(raw) {
		if path := downloadTwitterVideoFile(ctx, raw); path != "" {
			return path
		}
	}
	return downloadYTDLPVideoFile(ctx, raw)
}

func downloadYTDLPVideoFile(ctx context.Context, raw string) string {
	if _, err := exec.LookPath("yt-dlp"); err != nil {
		return ""
	}
	workDir, err := os.MkdirTemp("", "diana-resolver-video-*")
	if err != nil {
		return ""
	}
	outputPattern := filepath.Join(workDir, "video.%(ext)s")
	cmdCtx, cancel := context.WithTimeout(ctx, defaultPlatformTimeout)
	defer cancel()
	cmd := exec.CommandContext(cmdCtx, "yt-dlp", ytDLPFullVideoDownloadArgs(outputPattern, raw)...)
	if output, err := cmd.CombinedOutput(); err != nil {
		log.Printf("resolver yt-dlp video download failed for %s: %v: %s", raw, err, strings.TrimSpace(string(output)))
		_ = os.RemoveAll(workDir)
		return ""
	}
	matches, err := filepath.Glob(filepath.Join(workDir, "video.*"))
	if err != nil || len(matches) == 0 {
		_ = os.RemoveAll(workDir)
		return ""
	}
	sort.Strings(matches)
	path := matches[0]
	if !videoFileAllowed(path) {
		_ = os.RemoveAll(workDir)
		return ""
	}
	return path
}

func ytDLPFullVideoDownloadArgs(outputPattern, raw string) []string {
	args := []string{
		"--no-playlist",
		"--quiet",
		"--no-warnings",
		"-f", "bv*[height<=720]+ba/b[height<=720]/best[height<=720]/best",
		"--merge-output-format", "mp4",
		"-o", outputPattern,
	}
	if maxMB := resolverVideoDownloadMaxMB(); maxMB > 0 {
		args = append(args, "--max-filesize", fmt.Sprintf("%dM", maxMB))
	}
	args = appendYTDLPResolverArgs(args, raw)
	return append(args, raw)
}

func appendYTDLPResolverArgs(args []string, raw string) []string {
	if cookies := strings.TrimSpace(os.Getenv("DIANA_YTDLP_COOKIES")); cookies != "" {
		args = append(args, "--cookies", cookies)
	} else if cookies := defaultYTDLPCookiesPath(); cookies != "" {
		args = append(args, "--cookies", cookies)
	}
	if browser := strings.TrimSpace(os.Getenv("DIANA_YTDLP_COOKIES_FROM_BROWSER")); browser != "" {
		args = append(args, "--cookies-from-browser", browser)
	}
	if proxy := firstNonEmpty(os.Getenv("DIANA_RESOLVER_PROXY"), os.Getenv("RESOLVER_PROXY")); proxy != "" {
		args = append(args, "--proxy", proxy)
	}
	if sessdata := bilibiliSessdata(); sessdata != "" && isBilibiliURL(raw) {
		args = append(args, "--add-header", "Cookie: SESSDATA="+sessdata)
	}
	if cookie := platformCookieHeader(raw); cookie != "" {
		args = append(args, "--add-header", "Cookie: "+cookie)
	}
	return args
}

func defaultYTDLPCookiesPath() string {
	path, err := filepath.Abs("ytb_cookies.txt")
	if err != nil {
		return ""
	}
	info, err := os.Stat(path)
	if err != nil || info.IsDir() || info.Size() == 0 {
		return ""
	}
	return path
}

func downloadDouyinVideoFile(ctx context.Context, raw string) string {
	cookie := strings.TrimSpace(firstNonEmpty(os.Getenv("DIANA_DOUYIN_CK"), os.Getenv("DOUYIN_CK"), os.Getenv("douyin_ck")))
	if cookie == "" {
		return ""
	}
	pageURL := fetchFinalURL(ctx, raw, resolverCommonHeaders())
	if pageURL == "" {
		pageURL = raw
	}
	match := douyinIDPattern.FindStringSubmatch(pageURL)
	if len(match) < 2 {
		return ""
	}
	awemeID := match[1]
	headers := resolverCommonHeaders()
	headers["Accept-Language"] = "zh-CN,zh;q=0.8,zh-TW;q=0.7,zh-HK;q=0.5,en-US;q=0.3,en;q=0.2"
	headers["Referer"] = "https://www.douyin.com/video/" + awemeID
	headers["Cookie"] = cookie
	apiURL := fmt.Sprintf(douyinVideoAPI, awemeID)
	if bogus := generateDouyinABogus(ctx, apiURL, headers["User-Agent"]); bogus != "" {
		apiURL += "&a_bogus=" + url.QueryEscape(bogus)
	}
	var detail struct {
		AwemeDetail struct {
			AwemeType int `json:"aweme_type"`
			Video     struct {
				PlayAddr struct {
					URI string `json:"uri"`
				} `json:"play_addr"`
			} `json:"video"`
		} `json:"aweme_detail"`
	}
	if !fetchResolverJSON(ctx, apiURL, headers, &detail) {
		return ""
	}
	if detail.AwemeDetail.AwemeType == 2 || detail.AwemeDetail.AwemeType == 68 {
		return ""
	}
	uri := strings.TrimSpace(detail.AwemeDetail.Video.PlayAddr.URI)
	if uri == "" {
		return ""
	}
	return downloadGenericVideoFile(ctx, fmt.Sprintf(douyinPlayURL, uri), resolverCommonHeaders())
}

func generateDouyinABogus(ctx context.Context, raw string, userAgent string) string {
	if _, err := exec.LookPath("node"); err != nil {
		return ""
	}
	parsed, err := url.Parse(raw)
	if err != nil {
		return ""
	}
	cmdCtx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	script := douyinABogusJS + "\nconsole.log(generate_a_bogus(process.argv[1], process.argv[2]));\n"
	output, err := exec.CommandContext(cmdCtx, "node", "-e", script, parsed.RawQuery, userAgent).Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(output))
}

func downloadXiaohongshuVideoFile(ctx context.Context, raw string) string {
	note, status := fetchXiaohongshuNote(ctx, raw)
	if status != "" {
		return ""
	}
	if strings.TrimSpace(anyString(note["type"])) != "video" {
		return ""
	}
	for _, videoURL := range xiaohongshuVideoCandidateURLs(note) {
		if path := downloadGenericVideoFile(ctx, videoURL, xiaohongshuVideoDownloadHeaders()); path != "" {
			return path
		}
	}
	return ""
}

func downloadTwitterVideoFile(ctx context.Context, raw string) string {
	apiURL := configuredTwitterResolverURL(ctx, raw)
	if apiURL == "" {
		return ""
	}
	headers := resolverCommonHeaders()
	headers["Accept"] = "ext/html,application/xhtml+xml,application/xml;q=0.9,image/avif,image/webp,image/apng,*/*;q=0.8,application/signed-exchange;v=b3;q=0.7"
	headers["Accept-Encoding"] = "gzip, deflate"
	headers["Accept-Language"] = "zh-CN,zh;q=0.9"
	var resp struct {
		Data struct {
			URL string `json:"url"`
		} `json:"data"`
	}
	if !fetchResolverJSON(ctx, apiURL, headers, &resp) || strings.TrimSpace(resp.Data.URL) == "" {
		return ""
	}
	mediaURL := strings.TrimSpace(resp.Data.URL)
	if resolverMediaURLIsImage(mediaURL) {
		return ""
	}
	return downloadGenericVideoFile(ctx, mediaURL, resolverCommonHeaders())
}

func downloadBilibiliVideoFile(ctx context.Context, raw string) string {
	if path := downloadBilibiliVideoFileViaAPI(ctx, raw); path != "" {
		return path
	}
	info, ok := ytdlpDumpInfo(ctx, raw)
	if !ok {
		return ""
	}
	if info.Duration > 0 && info.Duration > float64(resolverVideoMaxDuration()) {
		log.Printf("resolver bilibili video too long: %.1fs > %ds", info.Duration, resolverVideoMaxDuration())
		return ""
	}
	video, audio := selectBilibiliFormats(info)
	if video.URL == "" {
		return ""
	}
	workDir, err := os.MkdirTemp("", "diana-bili-video-*")
	if err != nil {
		return ""
	}
	videoPath := filepath.Join(workDir, "video.m4s")
	audioPath := filepath.Join(workDir, "audio.m4s")
	outputPath := filepath.Join(workDir, "video.mp4")
	headers := bilibiliDownloadHeaders(info.HTTPHeaders)
	if !downloadURLToFile(ctx, video.URL, videoPath, mergeHeaders(headers, video.HTTPHeaders), resolverVideoDownloadMaxBytes()) {
		_ = os.RemoveAll(workDir)
		return ""
	}
	if audio.URL != "" {
		if !downloadURLToFile(ctx, audio.URL, audioPath, mergeHeaders(headers, audio.HTTPHeaders), resolverVideoDownloadMaxBytes()) {
			_ = os.RemoveAll(workDir)
			return ""
		}
		if !mergeMediaToMP4(ctx, videoPath, audioPath, outputPath) {
			_ = os.RemoveAll(workDir)
			return ""
		}
	} else {
		outputPath = videoPath
	}
	if !videoFileAllowed(outputPath) {
		_ = os.RemoveAll(workDir)
		return ""
	}
	return outputPath
}

func downloadBilibiliVideoFileViaAPI(ctx context.Context, raw string) string {
	view, ok := fetchBilibiliView(ctx, raw)
	if !ok || view.Data.CID == 0 {
		return ""
	}
	cid := view.Data.CID
	duration := view.Data.Duration
	page := bilibiliPageFromURL(raw)
	if page > 0 && page <= len(view.Data.Pages) {
		if view.Data.Pages[page-1].CID != 0 {
			cid = view.Data.Pages[page-1].CID
		}
		if view.Data.Pages[page-1].Duration > 0 {
			duration = view.Data.Pages[page-1].Duration
		}
	}
	if duration > 0 && duration > resolverVideoMaxDuration() {
		log.Printf("resolver bilibili video too long: %ds > %ds", duration, resolverVideoMaxDuration())
		return ""
	}
	play, ok := fetchBilibiliPlayURL(ctx, view.Data.BVID, cid)
	if !ok {
		return ""
	}
	workDir, err := os.MkdirTemp("", "diana-bili-video-*")
	if err != nil {
		return ""
	}
	outputPath := filepath.Join(workDir, "video.mp4")
	headers := bilibiliDownloadHeaders(nil)
	video, audio := selectBilibiliDashMedia(play.Data.Dash.Video, play.Data.Dash.Audio)
	if video.Base() != "" {
		videoPath := filepath.Join(workDir, "video.m4s")
		audioPath := filepath.Join(workDir, "audio.m4s")
		if !downloadBilibiliCandidateURLs(ctx, video.URLs(), videoPath, headers, resolverVideoDownloadMaxBytes()) {
			_ = os.RemoveAll(workDir)
			return ""
		}
		if audio.Base() != "" {
			if !downloadBilibiliCandidateURLs(ctx, audio.URLs(), audioPath, headers, resolverVideoDownloadMaxBytes()) {
				_ = os.RemoveAll(workDir)
				return ""
			}
			if !mergeMediaToMP4(ctx, videoPath, audioPath, outputPath) {
				_ = os.RemoveAll(workDir)
				return ""
			}
		} else {
			outputPath = videoPath
		}
	} else if len(play.Data.DURL) > 0 {
		if !downloadBilibiliCandidateURLs(ctx, append([]string{play.Data.DURL[0].URL}, play.Data.DURL[0].BackupURL...), outputPath, headers, resolverVideoDownloadMaxBytes()) {
			_ = os.RemoveAll(workDir)
			return ""
		}
	} else {
		_ = os.RemoveAll(workDir)
		return ""
	}
	if !videoFileAllowed(outputPath) {
		_ = os.RemoveAll(workDir)
		return ""
	}
	return outputPath
}

func fetchBilibiliView(ctx context.Context, raw string) (bilibiliViewResponse, bool) {
	pageURL := resolveBilibiliURL(ctx, raw)
	bvid := bilibiliBVID(pageURL)
	if bvid == "" {
		return bilibiliViewResponse{}, false
	}
	apiURL := "https://api.bilibili.com/x/web-interface/view?bvid=" + url.QueryEscape(bvid)
	var resp bilibiliViewResponse
	if !fetchBilibiliJSON(ctx, apiURL, pageURL, &resp) || resp.Code != 0 {
		log.Printf("resolver bilibili view failed: code=%d message=%s", resp.Code, resp.Message)
		return bilibiliViewResponse{}, false
	}
	return resp, true
}

func fetchBilibiliPlayURL(ctx context.Context, bvid string, cid int64) (bilibiliPlayURLResponse, bool) {
	values := url.Values{}
	values.Set("bvid", bvid)
	values.Set("cid", strconv.FormatInt(cid, 10))
	values.Set("qn", "64")
	values.Set("fnval", "4048")
	values.Set("fourk", "0")
	apiURL := "https://api.bilibili.com/x/player/playurl?" + values.Encode()
	referer := "https://www.bilibili.com/video/" + bvid + "/"
	var resp bilibiliPlayURLResponse
	if !fetchBilibiliJSON(ctx, apiURL, referer, &resp) || resp.Code != 0 {
		log.Printf("resolver bilibili playurl failed: code=%d message=%s", resp.Code, resp.Message)
		return bilibiliPlayURLResponse{}, false
	}
	return resp, true
}

func fetchBilibiliJSON(ctx context.Context, raw string, referer string, out any) bool {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, raw, nil)
	if err != nil {
		return false
	}
	for key, value := range bilibiliDownloadHeaders(nil) {
		req.Header.Set(key, value)
	}
	if strings.TrimSpace(referer) != "" {
		req.Header.Set("Referer", referer)
	}
	client := netguard.NewPublicHTTPClient(15 * time.Second)
	resp, err := client.Do(req)
	if err != nil {
		log.Printf("resolver bilibili api failed for %s: %v", redactURLQuery(raw), err)
		return false
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		log.Printf("resolver bilibili api bad status for %s: %s", redactURLQuery(raw), resp.Status)
		return false
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 4*1024*1024)).Decode(out); err != nil {
		log.Printf("resolver bilibili api json parse failed for %s: %v", redactURLQuery(raw), err)
		return false
	}
	return true
}

func resolveBilibiliURL(ctx context.Context, raw string) string {
	parsed, err := url.Parse(raw)
	if err != nil {
		return raw
	}
	if !hostMatchesDomain(parsed.Hostname(), "b23.tv", "bili2233.cn") {
		return raw
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodHead, raw, nil)
	if err != nil {
		return raw
	}
	for key, value := range bilibiliDownloadHeaders(nil) {
		req.Header.Set(key, value)
	}
	client := netguard.NewPublicHTTPClient(10 * time.Second)
	resp, err := client.Do(req)
	if err != nil {
		return raw
	}
	defer resp.Body.Close()
	if resp.Request != nil && resp.Request.URL != nil {
		return resp.Request.URL.String()
	}
	return raw
}

func bilibiliBVID(raw string) string {
	parsed, err := url.Parse(raw)
	if err != nil {
		return ""
	}
	for _, part := range strings.Split(parsed.Path, "/") {
		if strings.HasPrefix(part, "BV") && len(part) >= 12 {
			return part
		}
	}
	return ""
}

func bilibiliPageFromURL(raw string) int {
	parsed, err := url.Parse(raw)
	if err != nil {
		return 0
	}
	page, _ := strconv.Atoi(parsed.Query().Get("p"))
	return page
}

func selectBilibiliDashMedia(videos []bilibiliDashMedia, audios []bilibiliDashMedia) (bilibiliDashMedia, bilibiliDashMedia) {
	var video bilibiliDashMedia
	for _, candidate := range videos {
		if candidate.Base() == "" || candidate.Height > 720 {
			continue
		}
		if video.Base() == "" || preferBilibiliDashVideo(candidate, video) {
			video = candidate
		}
	}
	var audio bilibiliDashMedia
	for _, candidate := range audios {
		if candidate.Base() == "" {
			continue
		}
		if audio.Base() == "" || candidate.Bandwidth > audio.Bandwidth {
			audio = candidate
		}
	}
	return video, audio
}

func preferBilibiliDashVideo(candidate bilibiliDashMedia, current bilibiliDashMedia) bool {
	if candidate.Height != current.Height {
		return candidate.Height > current.Height
	}
	if strings.Contains(candidate.Codecs, "avc") != strings.Contains(current.Codecs, "avc") {
		return strings.Contains(candidate.Codecs, "avc")
	}
	return candidate.Bandwidth > current.Bandwidth
}

func (m bilibiliDashMedia) Base() string {
	return firstNonEmpty(m.BaseURL, m.BaseURL2)
}

func (m bilibiliDashMedia) URLs() []string {
	urls := []string{m.Base()}
	urls = append(urls, m.BackupURL...)
	urls = append(urls, m.BackupURL2...)
	out := urls[:0]
	seen := map[string]bool{}
	for _, raw := range urls {
		raw = strings.TrimSpace(raw)
		if raw == "" || seen[raw] {
			continue
		}
		seen[raw] = true
		out = append(out, raw)
	}
	return out
}

func downloadBilibiliCandidateURLs(ctx context.Context, urls []string, path string, headers map[string]string, maxBytes int64) bool {
	for _, raw := range urls {
		if downloadURLToFile(ctx, raw, path, headers, maxBytes) {
			return true
		}
		_ = os.Remove(path)
	}
	return false
}

func ytdlpDumpInfo(ctx context.Context, raw string) (ytdlpInfo, bool) {
	if _, err := exec.LookPath("yt-dlp"); err != nil {
		return ytdlpInfo{}, false
	}
	cmdCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	args := []string{"--simulate", "--dump-json", "--no-warnings"}
	args = appendYTDLPResolverArgs(args, raw)
	args = append(args, raw)
	cmd := exec.CommandContext(cmdCtx, "yt-dlp", args...)
	output, err := cmd.Output()
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			log.Printf("resolver yt-dlp dump failed for %s: %v: %s", raw, err, strings.TrimSpace(string(exitErr.Stderr)))
		} else {
			log.Printf("resolver yt-dlp dump failed for %s: %v", raw, err)
		}
		return ytdlpInfo{}, false
	}
	var info ytdlpInfo
	if err := json.Unmarshal(output, &info); err != nil {
		log.Printf("resolver yt-dlp dump json parse failed for %s: %v", raw, err)
		return ytdlpInfo{}, false
	}
	return info, true
}

func selectBilibiliFormats(info ytdlpInfo) (ytdlpFormat, ytdlpFormat) {
	var video ytdlpFormat
	var audio ytdlpFormat
	for _, format := range info.Formats {
		if format.URL == "" {
			continue
		}
		if format.VCodec != "" && format.VCodec != "none" && (format.ACodec == "" || format.ACodec == "none") {
			if format.Height <= 0 || format.Height > 720 {
				continue
			}
			if video.URL == "" || preferVideoFormat(format, video) {
				video = format
			}
			continue
		}
		if format.ACodec != "" && format.ACodec != "none" && (format.VCodec == "" || format.VCodec == "none") {
			if audio.URL == "" || formatSize(format) > formatSize(audio) {
				audio = format
			}
		}
	}
	if video.URL == "" {
		for _, format := range info.RequestedFormats {
			if format.VCodec != "" && format.VCodec != "none" {
				video = format
				break
			}
		}
	}
	if audio.URL == "" {
		for _, format := range info.RequestedFormats {
			if format.ACodec != "" && format.ACodec != "none" && (format.VCodec == "" || format.VCodec == "none") {
				audio = format
				break
			}
		}
	}
	return video, audio
}

func preferVideoFormat(candidate ytdlpFormat, current ytdlpFormat) bool {
	if candidate.Height != current.Height {
		return candidate.Height > current.Height
	}
	if strings.HasPrefix(candidate.VCodec, "avc") != strings.HasPrefix(current.VCodec, "avc") {
		return strings.HasPrefix(candidate.VCodec, "avc")
	}
	return formatSize(candidate) > formatSize(current)
}

func formatSize(format ytdlpFormat) int64 {
	if format.Filesize > 0 {
		return format.Filesize
	}
	return format.ApproxSize
}

func bilibiliDownloadHeaders(base map[string]string) map[string]string {
	headers := map[string]string{
		"User-Agent": "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/148.0.0.0 Safari/537.36",
		"Referer":    "https://www.bilibili.com",
	}
	if sessdata := bilibiliSessdata(); sessdata != "" {
		headers["Cookie"] = "SESSDATA=" + sessdata
	}
	for key, value := range base {
		if strings.TrimSpace(value) != "" {
			headers[key] = value
		}
	}
	return headers
}

func bilibiliSessdata() string {
	return firstNonEmpty(os.Getenv("DIANA_BILI_SESSDATA"), os.Getenv("BILI_SESSDATA"))
}

func platformCookieHeader(raw string) string {
	parsed, err := url.Parse(raw)
	if err != nil {
		return ""
	}
	host := parsed.Hostname()
	switch {
	case hostMatchesDomain(host, "douyin.com"):
		return firstNonEmpty(os.Getenv("DIANA_DOUYIN_CK"), os.Getenv("DOUYIN_CK"), os.Getenv("douyin_ck"))
	case hostMatchesDomain(host, "xiaohongshu.com", "xhslink.com"):
		return firstNonEmpty(os.Getenv("DIANA_XHS_CK"), os.Getenv("XHS_CK"), os.Getenv("xhs_ck"))
	default:
		return ""
	}
}

func resolverCommonHeaders() map[string]string {
	return map[string]string{
		"User-Agent": resolverUserAgent,
	}
}

func fetchFinalURL(ctx context.Context, raw string, headers map[string]string) string {
	finalURL, _, _ := fetchFinalURLDetails(ctx, raw, headers)
	return finalURL
}

func fetchFinalURLDetails(ctx context.Context, raw string, headers map[string]string) (string, int, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, raw, nil)
	if err != nil {
		return "", 0, err
	}
	for key, value := range headers {
		req.Header.Set(key, value)
	}
	client := netguard.NewPublicHTTPClient(12 * time.Second)
	resp, err := client.Do(req)
	if err != nil {
		return "", 0, err
	}
	defer resp.Body.Close()
	if resp.Request != nil && resp.Request.URL != nil {
		return resp.Request.URL.String(), resp.StatusCode, nil
	}
	return "", resp.StatusCode, nil
}

func fetchResolverJSON(ctx context.Context, raw string, headers map[string]string, target any) bool {
	body, ok := fetchResolverBody(ctx, raw, headers)
	if !ok {
		return false
	}
	if err := json.Unmarshal([]byte(body), target); err != nil {
		log.Printf("resolver json parse failed for %s: %v", redactURLQuery(raw), err)
		return false
	}
	return true
}

func fetchResolverBody(ctx context.Context, raw string, headers map[string]string) (string, bool) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, raw, nil)
	if err != nil {
		return "", false
	}
	for key, value := range headers {
		req.Header.Set(key, value)
	}
	client := netguard.NewPublicHTTPClient(defaultPlatformTimeout)
	resp, err := client.Do(req)
	if err != nil {
		log.Printf("resolver request failed for %s: %v", redactURLQuery(raw), err)
		return "", false
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		log.Printf("resolver request bad status for %s: %s", redactURLQuery(raw), resp.Status)
		return "", false
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, 4*1024*1024))
	if err != nil || len(data) == 0 {
		return "", false
	}
	return string(data), true
}

func downloadGenericVideoFile(ctx context.Context, raw string, headers map[string]string) string {
	workDir, err := os.MkdirTemp("", "diana-resolver-video-*")
	if err != nil {
		return ""
	}
	outputPath := filepath.Join(workDir, "video.mp4")
	if !downloadURLToFile(ctx, raw, outputPath, headers, resolverVideoDownloadMaxBytes()) || !videoFileAllowed(outputPath) {
		_ = os.RemoveAll(workDir)
		return ""
	}
	return outputPath
}

func xiaohongshuRequestParts(raw string) (id string, xsecSource string, xsecToken string) {
	raw = html.UnescapeString(strings.TrimSpace(raw))
	parsed, err := url.Parse(raw)
	if err != nil {
		return "", "", ""
	}
	parts := strings.Split(strings.Trim(parsed.Path, "/"), "/")
	for i := 0; i+1 < len(parts); i++ {
		if parts[i] == "explore" || (parts[i] == "discovery" && parts[i+1] == "item" && i+2 < len(parts)) {
			if parts[i] == "discovery" {
				id = parts[i+2]
			} else {
				id = parts[i+1]
			}
			break
		}
	}
	query := parsed.Query()
	if id == "" {
		id = firstNonEmpty(query.Get("noteId"), query.Get("note_id"))
	}
	xsecSource = firstNonEmpty(query.Get("xsec_source"), "pc_feed")
	xsecToken = query.Get("xsec_token")
	return id, xsecSource, xsecToken
}

func fetchXiaohongshuNote(ctx context.Context, raw string) (map[string]any, string) {
	raw = html.UnescapeString(strings.TrimSpace(raw))
	cookie := strings.TrimSpace(firstNonEmpty(os.Getenv("DIANA_XHS_CK"), os.Getenv("XHS_CK"), os.Getenv("xhs_ck")))
	if cookie == "" {
		return nil, "missing_cookie"
	}
	headers := xiaohongshuPageHeaders(cookie)
	pageURL := raw
	if urlMatchesDomain(raw, "xhslink.com") {
		finalURL, statusCode, err := fetchFinalURLDetails(ctx, raw, headers)
		if err != nil {
			return nil, "request_failed"
		}
		if statusCode == http.StatusNotFound || statusCode == http.StatusGone {
			return nil, "expired_link"
		}
		if statusCode < 200 || statusCode >= 400 {
			return nil, "request_failed"
		}
		if finalURL != "" {
			pageURL = finalURL
		}
	}
	xhsID, xsecSource, xsecToken := xiaohongshuRequestParts(pageURL)
	if xhsID == "" {
		if isXiaohongshuLiveURL(pageURL) || isXiaohongshuLiveURL(raw) {
			return nil, "live_link"
		}
		return nil, "unsupported_link"
	}
	reqURL := fmt.Sprintf(xiaohongshuExploreURL, xhsID, url.QueryEscape(firstNonEmpty(xsecSource, "pc_feed")), url.QueryEscape(xsecToken))
	body, ok := fetchResolverBody(ctx, reqURL, headers)
	if !ok {
		return nil, "request_failed"
	}
	match := xiaohongshuStateRegex.FindStringSubmatch(body)
	if len(match) < 2 {
		return nil, "page_unavailable"
	}
	stateJSON := strings.ReplaceAll(match[1], "undefined", "null")
	var state map[string]any
	if err := json.Unmarshal([]byte(stateJSON), &state); err != nil {
		return nil, "page_unavailable"
	}
	note := xiaohongshuNoteData(state, xhsID)
	if len(note) == 0 {
		return nil, "note_unavailable"
	}
	return note, ""
}

func isXiaohongshuLiveURL(raw string) bool {
	parsed, err := url.Parse(html.UnescapeString(strings.TrimSpace(raw)))
	if err != nil {
		return false
	}
	host := strings.ToLower(parsed.Hostname())
	path := strings.ToLower(parsed.Path)
	return (hostMatchesDomain(host, "xhslink.com") && strings.HasPrefix(path, "/m/")) ||
		(hostMatchesDomain(host, "xiaohongshu.com") && (strings.Contains(path, "/live/") || strings.Contains(path, "/livestream/")))
}

func xiaohongshuPageHeaders(cookie string) map[string]string {
	headers := resolverCommonHeaders()
	headers["Accept"] = "text/html,application/xhtml+xml,application/xml;q=0.9,image/webp,image/apng,*/*;q=0.8,application/signed-exchange;v=b3;q=0.9"
	headers["Cookie"] = cookie
	return headers
}

func xiaohongshuVideoDownloadHeaders() map[string]string {
	headers := resolverCommonHeaders()
	headers["Referer"] = "https://www.xiaohongshu.com/"
	headers["Origin"] = "https://www.xiaohongshu.com"
	return headers
}

func xiaohongshuNoteData(state map[string]any, xhsID string) map[string]any {
	noteRoot, _ := state["note"].(map[string]any)
	detailMap, _ := noteRoot["noteDetailMap"].(map[string]any)
	detail, _ := detailMap[xhsID].(map[string]any)
	note, _ := detail["note"].(map[string]any)
	return note
}

func xiaohongshuVideoCandidateURLs(note map[string]any) []string {
	video, _ := note["video"].(map[string]any)
	media, _ := video["media"].(map[string]any)
	stream, _ := media["stream"].(map[string]any)
	h264, _ := stream["h264"].([]any)
	out := make([]string, 0, len(h264)*2)
	seen := map[string]bool{}
	for _, item := range h264 {
		streamItem, _ := item.(map[string]any)
		candidates := []string{anyString(streamItem["masterUrl"])}
		if backups, _ := streamItem["backupUrls"].([]any); len(backups) > 0 {
			for _, backup := range backups {
				candidates = append(candidates, anyString(backup))
			}
		}
		for _, candidate := range candidates {
			candidate = strings.TrimSpace(candidate)
			if candidate == "" || seen[candidate] {
				continue
			}
			seen[candidate] = true
			out = append(out, candidate)
		}
	}
	return out
}

func xiaohongshuVideoMasterURL(note map[string]any) string {
	return firstNonEmptyString(xiaohongshuVideoCandidateURLs(note))
}

func anyString(value any) string {
	switch typed := value.(type) {
	case string:
		return typed
	case fmt.Stringer:
		return typed.String()
	default:
		return ""
	}
}

func mergeHeaders(base map[string]string, extra map[string]string) map[string]string {
	headers := map[string]string{}
	for key, value := range base {
		headers[key] = value
	}
	for key, value := range extra {
		if strings.TrimSpace(value) != "" {
			headers[key] = value
		}
	}
	return headers
}

func downloadURLToFile(ctx context.Context, raw string, path string, headers map[string]string, maxBytes int64) bool {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, raw, nil)
	if err != nil {
		return false
	}
	for key, value := range headers {
		req.Header.Set(key, value)
	}
	client := netguard.NewPublicHTTPClient(defaultPlatformTimeout)
	resp, err := client.Do(req)
	if err != nil {
		log.Printf("resolver media download failed for %s: %v", redactURLQuery(raw), err)
		return false
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		log.Printf("resolver media download bad status for %s: %s", redactURLQuery(raw), resp.Status)
		return false
	}
	if maxBytes > 0 && resp.ContentLength > maxBytes {
		log.Printf("resolver media too large for %s: %d > %d", redactURLQuery(raw), resp.ContentLength, maxBytes)
		return false
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return false
	}
	defer file.Close()
	var written int64
	if maxBytes > 0 {
		written, err = io.Copy(file, io.LimitReader(resp.Body, maxBytes+1))
	} else {
		written, err = io.Copy(file, resp.Body)
	}
	if err != nil || written == 0 || (maxBytes > 0 && written > maxBytes) {
		log.Printf("resolver media write failed for %s: written=%d err=%v", redactURLQuery(raw), written, err)
		return false
	}
	return true
}

func mergeMediaToMP4(ctx context.Context, videoPath string, audioPath string, outputPath string) bool {
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		return false
	}
	cmdCtx, cancel := context.WithTimeout(ctx, 45*time.Second)
	defer cancel()
	cmd := exec.CommandContext(cmdCtx, "ffmpeg", "-hide_banner", "-loglevel", "error", "-y", "-i", videoPath, "-i", audioPath, "-c", "copy", outputPath)
	if output, err := cmd.CombinedOutput(); err != nil {
		log.Printf("resolver ffmpeg merge failed: %v: %s", err, strings.TrimSpace(string(output)))
		return false
	}
	return true
}

func videoFileAllowed(path string) bool {
	info, err := os.Stat(path)
	if err != nil || info.IsDir() || info.Size() == 0 {
		return false
	}
	if maxBytes := resolverVideoDownloadMaxBytes(); maxBytes > 0 && info.Size() > maxBytes {
		return false
	}
	return true
}

func resolverVideoMaxMB() int {
	return envInt("DIANA_RESOLVER_VIDEO_MAX_MB", defaultVideoMaxMB)
}

type resolverVideoUpload struct {
	Path   string
	Name   string
	SizeMB float64
}

func splitResolverVideoUploads(videoURLs []string) ([]string, []resolverVideoUpload) {
	direct := make([]string, 0, len(videoURLs))
	uploads := make([]resolverVideoUpload, 0, 1)
	for _, videoURL := range videoURLs {
		path := localMediaPath(videoURL)
		if path == "" {
			direct = append(direct, videoURL)
			continue
		}
		info, err := os.Stat(path)
		if err != nil || info.IsDir() {
			direct = append(direct, videoURL)
			continue
		}
		uploads = append(uploads, resolverVideoUploadFromInfo(path, info))
	}
	return direct, uploads
}

func resolverVideoUploadFromPath(path string) (resolverVideoUpload, bool) {
	info, err := os.Stat(path)
	if err != nil || info.IsDir() {
		return resolverVideoUpload{}, false
	}
	return resolverVideoUploadFromInfo(path, info), true
}

func resolverVideoUploadFromInfo(path string, info os.FileInfo) resolverVideoUpload {
	return resolverVideoUpload{
		Path:   path,
		Name:   filepath.Base(path),
		SizeMB: float64(info.Size()) / 1024 / 1024,
	}
}

func dedupeResolverVideoUploads(uploads []resolverVideoUpload) []resolverVideoUpload {
	out := make([]resolverVideoUpload, 0, len(uploads))
	seen := map[string]bool{}
	for _, upload := range uploads {
		key := strings.TrimSpace(upload.Path)
		if key == "" || seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, upload)
	}
	return out
}

func localMediaPath(value string) string {
	value = strings.TrimSpace(strings.TrimPrefix(value, "file://"))
	if value == "" || !filepath.IsAbs(value) {
		return ""
	}
	return value
}

func resolverVideoDownloadMaxBytes() int64 {
	maxMB := resolverVideoDownloadMaxMB()
	if maxMB <= 0 {
		return 0
	}
	return int64(maxMB) * 1024 * 1024
}

func resolverVideoDownloadMaxMB() int {
	if strings.TrimSpace(os.Getenv("DIANA_RESOLVER_VIDEO_DOWNLOAD_MAX_MB")) != "" {
		return envInt("DIANA_RESOLVER_VIDEO_DOWNLOAD_MAX_MB", resolverVideoMaxMB())
	}
	return resolverVideoMaxMB()
}

func resolverVideoMaxDuration() int {
	return envInt("DIANA_RESOLVER_VIDEO_MAX_DURATION", defaultVideoMaxDuration)
}

func envInt(key string, fallback int) int {
	value := strings.TrimSpace(os.Getenv(key))
	if value == "" {
		return fallback
	}
	parsed, err := strconv.Atoi(value)
	if err != nil || parsed <= 0 {
		return fallback
	}
	return parsed
}

func isBilibiliURL(raw string) bool {
	parsed, err := url.Parse(raw)
	if err != nil {
		return false
	}
	return hostMatchesDomain(parsed.Hostname(), "bilibili.com", "b23.tv", "bili2233.cn")
}

func isDouyinURL(raw string) bool {
	parsed, err := url.Parse(raw)
	if err != nil {
		return false
	}
	return hostMatchesDomain(parsed.Hostname(), "douyin.com")
}

func isXiaohongshuURL(raw string) bool {
	parsed, err := url.Parse(raw)
	if err != nil {
		return false
	}
	return hostMatchesDomain(parsed.Hostname(), "xiaohongshu.com", "xhslink.com")
}

func isTwitterURL(raw string) bool {
	parsed, err := url.Parse(raw)
	if err != nil {
		return false
	}
	return hostMatchesDomain(parsed.Hostname(), "x.com", "twitter.com")
}

func redactURLQuery(raw string) string {
	parsed, err := url.Parse(raw)
	if err != nil {
		return raw
	}
	parsed.RawQuery = ""
	return parsed.String()
}

func cleanupLocalMediaFilesLater(paths []string, delay time.Duration) {
	var local []string
	for _, path := range paths {
		path = strings.TrimSpace(strings.TrimPrefix(path, "file://"))
		if path != "" && filepath.IsAbs(path) {
			local = append(local, path)
		}
	}
	if len(local) == 0 {
		return
	}
	go func() {
		time.Sleep(delay)
		for _, path := range local {
			_ = os.Remove(path)
			_ = os.Remove(path + ".jpg")
			_ = os.RemoveAll(filepath.Dir(path))
		}
	}()
}
