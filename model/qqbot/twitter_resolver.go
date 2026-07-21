package qqbot

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

const (
	defaultTwitterMinimumGroupLevel = 40
	defaultResolverImageMaxMB       = 25
	maximumTwitterMediaItems        = 8
	defaultTwitterMetadataAPI       = "https://api.fxtwitter.com/status/{id}"
)

type twitterPost struct {
	Text         string
	AuthorName   string
	AuthorHandle string
	Media        []twitterMedia
}

type twitterMedia struct {
	Type         string
	URL          string
	TranscodeURL string
	ThumbnailURL string
	Duration     float64
	Formats      []twitterMediaFormat
}

type twitterMediaFormat struct {
	URL       string `json:"url"`
	Container string `json:"container"`
	Bitrate   int64  `json:"bitrate"`
}

type twitterResponseEnvelope struct {
	Tweet  json.RawMessage `json:"tweet"`
	Status json.RawMessage `json:"status"`
	Data   json.RawMessage `json:"data"`
}

type twitterPostPayload struct {
	URL         string                `json:"url"`
	Text        string                `json:"text"`
	FullText    string                `json:"full_text"`
	Description string                `json:"description"`
	Desc        string                `json:"desc"`
	Content     string                `json:"content"`
	Caption     string                `json:"caption"`
	Author      json.RawMessage       `json:"author"`
	User        json.RawMessage       `json:"user"`
	Media       json.RawMessage       `json:"media"`
	Images      []twitterMediaPayload `json:"images"`
	Videos      []twitterMediaPayload `json:"videos"`
	GIFs        []twitterMediaPayload `json:"gifs"`
}

type twitterMediaContainerPayload struct {
	All    []twitterMediaPayload `json:"all"`
	Photos []twitterMediaPayload `json:"photos"`
	Videos []twitterMediaPayload `json:"videos"`
}

type twitterMediaPayload struct {
	Type         string               `json:"type"`
	URL          string               `json:"url"`
	MediaURL     string               `json:"media_url"`
	MediaURLHTTP string               `json:"media_url_https"`
	TranscodeURL string               `json:"transcode_url"`
	ThumbnailURL string               `json:"thumbnail_url"`
	Duration     float64              `json:"duration"`
	Formats      []twitterMediaFormat `json:"formats"`
	Variants     []struct {
		URL         string `json:"url"`
		Bitrate     int64  `json:"bitrate"`
		ContentType string `json:"content_type"`
	} `json:"variants"`
}

func fetchTwitterPost(ctx context.Context, raw string) (twitterPost, bool) {
	var legacy twitterPost
	if apiURL := configuredTwitterResolverURL(ctx, raw); apiURL != "" {
		if body, ok := fetchResolverBody(ctx, apiURL, twitterResolverHeaders()); ok {
			if post, parsed := parseTwitterPostResponse([]byte(body)); parsed {
				if twitterPostHasStructuredMetadata(post) {
					return post, true
				}
				legacy = post
			}
		}
	}

	if apiURL := twitterMetadataAPIURL(raw); apiURL != "" {
		if body, ok := fetchResolverBody(ctx, apiURL, twitterResolverHeaders()); ok {
			if post, parsed := parseTwitterPostResponse([]byte(body)); parsed {
				if len(post.Media) == 0 && len(legacy.Media) > 0 {
					post.Media = legacy.Media
				}
				return post, true
			}
		}
	}
	return legacy, twitterPostHasContent(legacy)
}

func twitterResolverHeaders() map[string]string {
	headers := resolverCommonHeaders()
	headers["Accept"] = "application/json"
	headers["Referer"] = "https://x.com/"
	return headers
}

func twitterMetadataAPIURL(raw string) string {
	id := twitterStatusID(raw)
	if id == "" {
		return ""
	}
	template := strings.TrimSpace(os.Getenv("DIANA_TWITTER_METADATA_API"))
	if template == "" {
		template = defaultTwitterMetadataAPI
	}
	switch {
	case strings.Contains(template, "{id}"):
		template = strings.ReplaceAll(template, "{id}", url.PathEscape(id))
	case strings.Contains(template, "{url}"):
		template = strings.ReplaceAll(template, "{url}", url.QueryEscape(raw))
	default:
		parsed, err := url.Parse(template)
		if err != nil {
			return ""
		}
		parsed.Path = strings.TrimRight(parsed.Path, "/") + "/" + url.PathEscape(id)
		template = parsed.String()
	}
	parsed, err := url.Parse(template)
	if err != nil || parsed.Scheme != "https" {
		return ""
	}
	return parsed.String()
}

func twitterStatusID(raw string) string {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || !hostMatchesDomain(parsed.Hostname(), "x.com", "twitter.com") {
		return ""
	}
	parts := strings.Split(strings.Trim(parsed.Path, "/"), "/")
	for index := 0; index+1 < len(parts); index++ {
		if !strings.EqualFold(parts[index], "status") {
			continue
		}
		id := strings.TrimSpace(parts[index+1])
		if id != "" && allASCIIDigits(id) {
			return id
		}
	}
	return ""
}

func allASCIIDigits(value string) bool {
	if value == "" {
		return false
	}
	for _, char := range value {
		if char < '0' || char > '9' {
			return false
		}
	}
	return true
}

func parseTwitterPostResponse(data []byte) (twitterPost, bool) {
	var envelope twitterResponseEnvelope
	if err := json.Unmarshal(data, &envelope); err != nil {
		return twitterPost{}, false
	}
	candidates := []json.RawMessage{envelope.Tweet, envelope.Status, envelope.Data, data}
	for _, candidate := range candidates {
		if len(candidate) == 0 || string(candidate) == "null" {
			continue
		}
		post, ok := parseTwitterPostPayload(candidate)
		if ok {
			return post, true
		}
	}
	return twitterPost{}, false
}

func parseTwitterPostPayload(data []byte) (twitterPost, bool) {
	var payload twitterPostPayload
	if err := json.Unmarshal(data, &payload); err != nil {
		return twitterPost{}, false
	}
	post := twitterPost{
		Text: firstNonEmpty(
			strings.TrimSpace(payload.Text),
			strings.TrimSpace(payload.FullText),
			strings.TrimSpace(payload.Description),
			strings.TrimSpace(payload.Desc),
			strings.TrimSpace(payload.Content),
			strings.TrimSpace(payload.Caption),
		),
	}
	post.AuthorName, post.AuthorHandle = parseTwitterAuthor(firstNonEmptyRawMessage(payload.Author, payload.User))
	post.Media = parseTwitterMediaContainer(payload.Media)
	if len(post.Media) == 0 {
		post.Media = appendTwitterMediaPayloads(post.Media, payload.Images, "photo")
		post.Media = appendTwitterMediaPayloads(post.Media, payload.Videos, "video")
		post.Media = appendTwitterMediaPayloads(post.Media, payload.GIFs, "gif")
	}
	if len(post.Media) == 0 && looksLikeTwitterMediaURL(payload.URL) {
		post.Media = append(post.Media, twitterMediaFromPayload(twitterMediaPayload{URL: payload.URL}, ""))
	}
	post.Media = dedupeTwitterMedia(post.Media)
	return post, twitterPostHasContent(post)
}

func firstNonEmptyRawMessage(values ...json.RawMessage) json.RawMessage {
	for _, value := range values {
		if len(value) > 0 && string(value) != "null" {
			return value
		}
	}
	return nil
}

func parseTwitterAuthor(data json.RawMessage) (string, string) {
	if len(data) == 0 {
		return "", ""
	}
	var text string
	if json.Unmarshal(data, &text) == nil && strings.TrimSpace(text) != "" {
		return strings.TrimSpace(text), ""
	}
	var author struct {
		Name       string `json:"name"`
		ScreenName string `json:"screen_name"`
		Username   string `json:"username"`
		Nickname   string `json:"nickname"`
	}
	if err := json.Unmarshal(data, &author); err != nil {
		return "", ""
	}
	return firstNonEmpty(strings.TrimSpace(author.Name), strings.TrimSpace(author.Nickname)),
		strings.TrimPrefix(firstNonEmpty(strings.TrimSpace(author.ScreenName), strings.TrimSpace(author.Username)), "@")
}

func parseTwitterMediaContainer(data json.RawMessage) []twitterMedia {
	if len(data) == 0 || string(data) == "null" {
		return nil
	}
	var direct []twitterMediaPayload
	if json.Unmarshal(data, &direct) == nil && len(direct) > 0 {
		return appendTwitterMediaPayloads(nil, direct, "")
	}
	var container twitterMediaContainerPayload
	if err := json.Unmarshal(data, &container); err != nil {
		return nil
	}
	if len(container.All) > 0 {
		return appendTwitterMediaPayloads(nil, container.All, "")
	}
	out := appendTwitterMediaPayloads(nil, container.Photos, "photo")
	return appendTwitterMediaPayloads(out, container.Videos, "video")
}

func appendTwitterMediaPayloads(dst []twitterMedia, values []twitterMediaPayload, fallbackType string) []twitterMedia {
	for _, value := range values {
		media := twitterMediaFromPayload(value, fallbackType)
		if media.downloadURL() != "" {
			dst = append(dst, media)
		}
	}
	return dst
}

func twitterMediaFromPayload(payload twitterMediaPayload, fallbackType string) twitterMedia {
	formats := append([]twitterMediaFormat(nil), payload.Formats...)
	for _, variant := range payload.Variants {
		container := ""
		if strings.Contains(strings.ToLower(variant.ContentType), "mp4") {
			container = "mp4"
		}
		formats = append(formats, twitterMediaFormat{URL: variant.URL, Container: container, Bitrate: variant.Bitrate})
	}
	media := twitterMedia{
		Type:         normalizeTwitterMediaType(firstNonEmpty(payload.Type, fallbackType)),
		URL:          firstNonEmpty(strings.TrimSpace(payload.URL), strings.TrimSpace(payload.MediaURLHTTP), strings.TrimSpace(payload.MediaURL)),
		TranscodeURL: strings.TrimSpace(payload.TranscodeURL),
		ThumbnailURL: strings.TrimSpace(payload.ThumbnailURL),
		Duration:     payload.Duration,
		Formats:      formats,
	}
	if media.Type == "" {
		switch {
		case resolverMediaURLIsImage(media.URL):
			media.Type = "photo"
		case media.URL != "":
			media.Type = "video"
		}
	}
	return media
}

func normalizeTwitterMediaType(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "photo", "image", "mosaic_photo":
		return "photo"
	case "video":
		return "video"
	case "gif", "animated_gif":
		return "gif"
	default:
		return ""
	}
}

func (m twitterMedia) downloadURL() string {
	if m.Type == "gif" && resolverMediaURLIsImage(m.TranscodeURL) {
		return strings.TrimSpace(m.TranscodeURL)
	}
	if strings.TrimSpace(m.URL) != "" {
		return strings.TrimSpace(m.URL)
	}
	formats := append([]twitterMediaFormat(nil), m.Formats...)
	sort.SliceStable(formats, func(i, j int) bool { return formats[i].Bitrate > formats[j].Bitrate })
	for _, format := range formats {
		if strings.TrimSpace(format.URL) == "" {
			continue
		}
		if format.Container == "" || strings.EqualFold(format.Container, "mp4") {
			return strings.TrimSpace(format.URL)
		}
	}
	return ""
}

func (m twitterMedia) sendAsImage() bool {
	return m.Type == "photo" || (m.Type == "gif" && resolverMediaURLIsImage(m.downloadURL()))
}

func dedupeTwitterMedia(values []twitterMedia) []twitterMedia {
	out := make([]twitterMedia, 0, len(values))
	seen := make(map[string]bool, len(values))
	for _, media := range values {
		mediaURL := media.downloadURL()
		if mediaURL == "" || seen[mediaURL] {
			continue
		}
		seen[mediaURL] = true
		out = append(out, media)
		if len(out) >= maximumTwitterMediaItems {
			break
		}
	}
	return out
}

func looksLikeTwitterMediaURL(raw string) bool {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") {
		return false
	}
	return hostMatchesDomain(parsed.Hostname(), "pbs.twimg.com", "video.twimg.com") ||
		resolverMediaURLIsImage(raw) || strings.EqualFold(filepath.Ext(parsed.Path), ".mp4")
}

func twitterPostHasStructuredMetadata(post twitterPost) bool {
	return strings.TrimSpace(post.Text) != "" || strings.TrimSpace(post.AuthorName) != "" ||
		strings.TrimSpace(post.AuthorHandle) != "" || len(post.Media) > 1
}

func twitterPostHasContent(post twitterPost) bool {
	return twitterPostHasStructuredMetadata(post) || len(post.Media) > 0
}

func twitterMetaText(nickname string, post twitterPost) string {
	lines := []string{fmt.Sprintf("%s识别：小蓝鸟学习版", nickname)}
	name := strings.TrimSpace(post.AuthorName)
	handle := strings.TrimSpace(post.AuthorHandle)
	switch {
	case name != "" && handle != "":
		lines = append(lines, fmt.Sprintf("作者：%s (@%s)", name, handle))
	case name != "":
		lines = append(lines, "作者："+name)
	case handle != "":
		lines = append(lines, "作者：@"+handle)
	}
	if text := strings.TrimSpace(post.Text); text != "" {
		lines = append(lines, "文案："+text)
	}
	return strings.Join(lines, "\n")
}

func twitterResolverRequestAllowed(ctx context.Context, req PluginRequest) bool {
	if req.Event.Kind != EventKindGroup {
		return true
	}
	if ownerID := strings.TrimSpace(req.OwnerID); ownerID != "" && ownerID == strings.TrimSpace(req.Event.UserID) {
		return true
	}
	minimum := twitterMinimumGroupLevel()
	if minimum <= 0 {
		return true
	}
	level, ok := parseQQGroupLevel(req.Event.SenderLevel)
	if !ok && req.Channel != nil {
		callCtx, cancel := context.WithTimeout(ctx, 4*time.Second)
		defer cancel()
		data, err := req.Channel.CallAPI(callCtx, "get_group_member_info", map[string]any{
			"group_id": oneBotIDParam(req.Event.GroupID),
			"user_id":  oneBotIDParam(req.Event.UserID),
			"no_cache": true,
		})
		if err == nil {
			level, ok = parseQQGroupLevel(qqGroupMemberInfoFromData(req.Event.GroupID, data).Level)
		}
	}
	return ok && level >= minimum
}

func twitterMinimumGroupLevel() int {
	value := strings.TrimSpace(os.Getenv("DIANA_TWITTER_MIN_GROUP_LEVEL"))
	if value == "" {
		return defaultTwitterMinimumGroupLevel
	}
	parsed, err := strconv.Atoi(value)
	if err != nil || parsed < 0 {
		return defaultTwitterMinimumGroupLevel
	}
	return parsed
}

func downloadTwitterMediaFile(ctx context.Context, media twitterMedia) string {
	mediaURL := media.downloadURL()
	if mediaURL == "" {
		return ""
	}
	if media.sendAsImage() {
		return downloadGenericImageFile(ctx, mediaURL, twitterResolverHeaders())
	}
	if media.Duration > 0 && media.Duration > float64(resolverVideoMaxDuration()) {
		return ""
	}
	return downloadGenericVideoFile(ctx, mediaURL, twitterResolverHeaders())
}

func downloadGenericImageFile(ctx context.Context, raw string, headers map[string]string) string {
	workDir, err := os.MkdirTemp("", "diana-resolver-image-*")
	if err != nil {
		return ""
	}
	temporaryPath := filepath.Join(workDir, "image.bin")
	if !downloadURLToFile(ctx, raw, temporaryPath, headers, resolverImageDownloadMaxBytes()) {
		_ = os.RemoveAll(workDir)
		return ""
	}
	extension := downloadedImageExtension(temporaryPath)
	if extension == "" {
		_ = os.RemoveAll(workDir)
		return ""
	}
	finalPath := filepath.Join(workDir, "image"+extension)
	if err := os.Rename(temporaryPath, finalPath); err != nil {
		_ = os.RemoveAll(workDir)
		return ""
	}
	return finalPath
}

func downloadedImageExtension(path string) string {
	file, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer file.Close()
	buffer := make([]byte, 512)
	count, err := file.Read(buffer)
	if err != nil || count == 0 {
		return ""
	}
	switch strings.ToLower(strings.TrimSpace(strings.Split(http.DetectContentType(buffer[:count]), ";")[0])) {
	case "image/jpeg":
		return ".jpg"
	case "image/png":
		return ".png"
	case "image/gif":
		return ".gif"
	case "image/webp":
		return ".webp"
	default:
		return ""
	}
}

func resolverImageDownloadMaxBytes() int64 {
	maxMB := envInt("DIANA_RESOLVER_IMAGE_MAX_MB", defaultResolverImageMaxMB)
	if maxMB <= 0 {
		return 0
	}
	return int64(maxMB) * 1024 * 1024
}
