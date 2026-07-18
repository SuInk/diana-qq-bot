package qqbot

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"rsc.io/pdf"
)

const (
	fileParserPluginID        = "official.file-parser-go"
	defaultFileParserMaxBytes = 32 * 1024 * 1024
	defaultFileParserMaxChars = 24000
)

type FileParserPlugin struct {
	client      *http.Client
	maxBytes    int64
	maxChars    int
	pdfRenderer pdfPageRenderer
}

type fileRef struct {
	Name      string
	URL       string
	LocalPath string
	FileID    string
	BusID     string
	GroupID   string
}

// NewFileParserPlugin 创建官方内置文件解析插件。
func NewFileParserPlugin(client *http.Client) *FileParserPlugin {
	if client == nil {
		client = &http.Client{Timeout: 15 * time.Second}
	}
	return &FileParserPlugin{
		client:      client,
		maxBytes:    defaultFileParserMaxBytes,
		maxChars:    defaultFileParserMaxChars,
		pdfRenderer: newWASMPDFRenderer(),
	}
}

// Manifest 返回文件解析插件清单。
func (p *FileParserPlugin) Manifest() PluginManifest {
	return PluginManifest{
		ID:          fileParserPluginID,
		Name:        "文件解析 Go",
		Version:     "0.3.0",
		Description: "官方内置文件解析插件；macOS 使用 PDFKit/Vision，本地原生路径不可用时回退沙盒 PDFium 和视觉 LLM。",
		Official:    true,
		BuiltIn:     true,
		Permissions: []string{"network:http", "message:read", "file:parse", "llm:multiple", "task:notify"},
	}
}

// Handle 解析消息里的文件并生成 LLM 上下文。
func (p *FileParserPlugin) Handle(ctx context.Context, req PluginRequest) (*PluginResponse, error) {
	refs := collectFileRefs(req)
	if len(refs) == 0 {
		return nil, nil
	}

	parts := make([]string, 0, len(refs))
	scannedDocuments := make([]scannedPDFDocument, 0, len(refs))
	for _, ref := range refs {
		result := p.parseRef(ctx, req.Channel, ref)
		if result.Context != "" {
			parts = append(parts, result.Context)
		}
		if result.ScannedPDF != nil {
			scannedDocuments = append(scannedDocuments, *result.ScannedPDF)
		}
	}
	contextText := ""
	if len(parts) > 0 {
		contextText = "文件解析结果：\n" + strings.Join(parts, "\n")
	}
	if len(scannedDocuments) > 0 {
		return &PluginResponse{
			Handled: true,
			Tasks: []PluginTask{
				newDocumentOCRTask(p.pdfRenderer, req.Text, scannedDocuments, contextText),
			},
		}, nil
	}
	return &PluginResponse{
		Handled: true,
		Context: contextText,
	}, nil
}

// collectFileRefs 从消息 segment 和文本链接中收集文件引用。
func collectFileRefs(req PluginRequest) []fileRef {
	refs := make([]fileRef, 0, 4)
	seen := map[string]struct{}{}

	add := func(ref fileRef) {
		ref.Name = strings.TrimSpace(ref.Name)
		ref.URL = strings.TrimSpace(ref.URL)
		ref.LocalPath = strings.TrimSpace(strings.TrimPrefix(ref.LocalPath, "file://"))
		ref.FileID = strings.TrimSpace(ref.FileID)
		ref.BusID = strings.TrimSpace(ref.BusID)
		ref.GroupID = strings.TrimSpace(ref.GroupID)
		if ref.Name == "" {
			ref.Name = fileNameFromURL(firstNonEmpty(ref.URL, ref.LocalPath, ref.FileID))
		}
		if ref.Name == "" || ref.Name == "." {
			ref.Name = "文件"
		}
		if !isSupportedFileName(ref.Name) && !isSupportedFileURL(ref.URL) && !isSupportedLocalFile(ref.LocalPath) {
			return
		}
		key := firstNonEmpty(ref.URL, ref.LocalPath, ref.FileID, ref.Name)
		if _, ok := seen[key]; ok {
			return
		}
		seen[key] = struct{}{}
		refs = append(refs, ref)
	}

	addURL := func(name string, rawURL string) {
		rawURL = strings.TrimSpace(rawURL)
		if rawURL == "" {
			return
		}
		if !isSupportedFileURL(rawURL) {
			return
		}
		add(fileRef{Name: name, URL: rawURL})
	}

	addSegments := func(groupID string, segments []MessageSegment) {
		for _, segment := range segments {
			if segment.Type != "file" {
				continue
			}
			name := firstNonEmpty(segment.Data["name"], segment.Data["filename"], segment.Data["file"])
			raw := firstNonEmpty(segment.Data["url"], segment.Data["download_url"], segment.Data["file_url"], segment.Data["file"], segment.Data["path"])
			ref := fileRef{
				Name:    name,
				FileID:  firstNonEmpty(segment.Data["file_id"], segment.Data["id"], segment.Data["fid"]),
				BusID:   firstNonEmpty(segment.Data["busid"], segment.Data["bus_id"]),
				GroupID: groupID,
				URL:     raw,
			}
			if normalizedFileURL(raw) != "" {
				ref.URL = raw
			} else if isSupportedLocalFile(raw) {
				ref.URL = ""
				ref.LocalPath = strings.TrimSpace(strings.TrimPrefix(raw, "file://"))
			} else {
				ref.URL = ""
			}
			add(ref)
		}
	}

	addSegments(req.Event.GroupID, req.Event.Segments)
	if req.Event.Quoted != nil {
		addSegments(firstNonEmpty(req.Event.Quoted.GroupID, req.Event.GroupID), req.Event.Quoted.Segments)
	}
	if shouldCollectRecentFileRefs(req.Text) {
		for _, event := range req.RecentEvents {
			addSegments(event.GroupID, event.Segments)
			if event.Quoted != nil {
				addSegments(firstNonEmpty(event.Quoted.GroupID, event.GroupID), event.Quoted.Segments)
			}
		}
	}

	for _, raw := range extractURLs(req.Text) {
		// 文本里的直链也当作候选文件，方便用户直接丢 txt/md/json 链接给机器人解析。
		addURL(fileNameFromURL(raw), raw)
	}

	return refs
}

func shouldCollectRecentFileRefs(text string) bool {
	text = strings.ToLower(strings.TrimSpace(text))
	if text == "" {
		return false
	}
	for _, marker := range []string{
		"文件", "文档", "附件", "资料", "讲义", "表格", "电子书", "压缩包",
		"pdf", "docx", "xlsx", "pptx", "txt", "markdown", "csv", "json", "epub",
		"file", "document", "attachment",
	} {
		if strings.Contains(text, marker) {
			return true
		}
	}
	return false
}

type parsedFileRef struct {
	Context    string
	ScannedPDF *scannedPDFDocument
}

// parseRef 下载并解析单个文件引用。
func (p *FileParserPlugin) parseRef(ctx context.Context, channel Channel, ref fileRef) parsedFileRef {
	ref = p.resolveOneBotFile(ctx, channel, ref)
	data, source, contentType, err := p.readRef(ctx, ref)
	if err != nil {
		return parsedFileRef{Context: fmt.Sprintf("- %s\n  地址：%s\n  状态：%v", ref.Name, source, err)}
	}
	if looksPDF(ref.Name, contentType, data) {
		nativeResult, nativeAvailable, nativeErr := runNativeMacPDF(ctx, data, "text", defaultOCRMaxPagesPerFile, defaultOCRPageMaxChars)
		if nativeAvailable && nativeErr == nil && nativeMacPDFHasText(nativeResult) {
			text := formatNativeMacPDFText(nativeResult, p.maxChars)
			return parsedFileRef{Context: fmt.Sprintf("- %s\n  地址：%s\n  类型：PDF（macOS PDFKit 文本层）\n  内容：\n%s", ref.Name, source, indentText(text, "  "))}
		}
		text := sanitizeFileTextString(extractPDFText(data, p.maxChars), p.maxChars)
		if pdfNeedsOCR(data, text) {
			hash := sha256.Sum256(data)
			return parsedFileRef{ScannedPDF: &scannedPDFDocument{
				Name:   ref.Name,
				Source: source,
				Data:   data,
				Hash:   hex.EncodeToString(hash[:]),
			}}
		}
		return parsedFileRef{Context: fmt.Sprintf("- %s\n  地址：%s\n  类型：PDF\n  内容：\n%s", ref.Name, source, indentText(text, "  "))}
	}
	if !looksTextual(ref.Name, contentType, data) {
		return parsedFileRef{Context: fmt.Sprintf("- %s\n  地址：%s\n  状态：暂不支持该文件类型", ref.Name, source)}
	}
	text := sanitizeFileText(data, p.maxChars)
	if text == "" {
		return parsedFileRef{Context: fmt.Sprintf("- %s\n  地址：%s\n  状态：未提取到文本", ref.Name, source)}
	}
	return parsedFileRef{Context: fmt.Sprintf("- %s\n  地址：%s\n  内容：\n%s", ref.Name, source, indentText(text, "  "))}
}

func (p *FileParserPlugin) readRef(ctx context.Context, ref fileRef) ([]byte, string, string, error) {
	if ref.URL != "" {
		data, contentType, err := p.readURL(ctx, ref.URL)
		return data, ref.URL, contentType, err
	}
	if ref.LocalPath != "" {
		data, contentType, err := p.readLocal(ref.LocalPath)
		return data, ref.LocalPath, contentType, err
	}
	return nil, firstNonEmpty(ref.Name, ref.FileID), "", fmt.Errorf("缺少可下载地址")
}

func (p *FileParserPlugin) resolveOneBotFile(ctx context.Context, channel Channel, ref fileRef) fileRef {
	if channel == nil || ref.FileID == "" || ref.URL != "" || ref.LocalPath != "" {
		return ref
	}
	callCtx, cancel := context.WithTimeout(ctx, 45*time.Second)
	defer cancel()
	requests := oneBotFileResolveRequests(ref)
	for _, req := range requests {
		data, err := channel.CallAPI(callCtx, req.action, req.params)
		if err == nil {
			if next := fileRefFromOneBotData(ref, data); next.URL != "" || next.LocalPath != "" {
				return next
			}
		}
	}
	if refreshed, ok := refreshOneBotFileFromGroupHistory(callCtx, channel, ref); ok {
		ref = refreshed
		if ref.URL != "" || ref.LocalPath != "" {
			return ref
		}
		requests = oneBotFileResolveRequests(ref)
	}
	for _, req := range requests {
		if req.action == "get_group_file_url" {
			continue
		}
		data, err := channel.CallAPI(callCtx, req.action, req.params)
		if err != nil {
			continue
		}
		if next := fileRefFromOneBotData(ref, data); next.URL != "" || next.LocalPath != "" {
			return next
		}
	}
	return ref
}

func refreshOneBotFileFromGroupHistory(ctx context.Context, channel Channel, ref fileRef) (fileRef, bool) {
	if strings.TrimSpace(ref.GroupID) == "" {
		return ref, false
	}
	messageSeq := ""
	for page := 0; page < 6; page++ {
		params := map[string]any{
			"group_id":        oneBotIDParam(ref.GroupID),
			"count":           100,
			"reverse_order":   messageSeq != "",
			"disable_get_url": true,
		}
		if messageSeq != "" {
			params["message_seq"] = messageSeq
		}
		data, err := channel.CallAPI(ctx, "get_group_msg_history", params)
		if err != nil {
			return ref, false
		}
		if next, ok := fileRefFromOneBotHistory(ref, data); ok {
			return next, true
		}
		nextSeq := oldestOneBotHistoryMessageID(data)
		if nextSeq == "" || nextSeq == messageSeq {
			break
		}
		messageSeq = nextSeq
	}
	return ref, false
}

func oldestOneBotHistoryMessageID(data map[string]any) string {
	for _, key := range []string{"messages", "items", "list"} {
		switch messages := data[key].(type) {
		case []any:
			if len(messages) > 0 {
				if message, ok := messages[0].(map[string]any); ok {
					return stringFromAny(message["message_id"])
				}
			}
		case []map[string]any:
			if len(messages) > 0 {
				return stringFromAny(messages[0]["message_id"])
			}
		}
	}
	if nested, ok := data["data"].(map[string]any); ok {
		return oldestOneBotHistoryMessageID(nested)
	}
	return ""
}

type oneBotFileResolveRequest struct {
	action string
	params map[string]any
}

func oneBotFileResolveRequests(ref fileRef) []oneBotFileResolveRequest {
	params := make([]oneBotFileResolveRequest, 0, 4)
	seen := map[string]struct{}{}
	appendRequest := func(action, key, value string, extra map[string]any) {
		value = strings.TrimSpace(value)
		if value == "" {
			return
		}
		dedupeKey := action + "\x00" + key + "\x00" + value
		if _, ok := seen[dedupeKey]; ok {
			return
		}
		seen[dedupeKey] = struct{}{}
		requestParams := map[string]any{key: value}
		for extraKey, extraValue := range extra {
			requestParams[extraKey] = extraValue
		}
		params = append(params, oneBotFileResolveRequest{action: action, params: requestParams})
	}
	if ref.GroupID != "" && strings.TrimSpace(ref.FileID) != "" {
		extra := map[string]any{"group_id": oneBotIDParam(ref.GroupID)}
		if ref.BusID != "" {
			extra["busid"] = ref.BusID
			extra["bus_id"] = ref.BusID
		}
		appendRequest("get_group_file_url", "file_id", ref.FileID, extra)
	}
	if name := strings.TrimSpace(ref.Name); name != "" && name != "文件" {
		// NapCat keeps the mapping behind incoming file IDs in memory. After a
		// restart, its local filename search can still recover an already cached file.
		appendRequest("get_file", "file", name, nil)
	}
	appendRequest("get_file", "file_id", ref.FileID, nil)
	appendRequest("get_file", "file", ref.FileID, nil)
	return params
}

func fileRefFromOneBotData(base fileRef, data map[string]any) fileRef {
	for _, key := range []string{"name", "file_name", "filename"} {
		if value := stringFromAny(data[key]); strings.TrimSpace(value) != "" && base.Name == "" {
			base.Name = value
		}
	}
	for _, key := range []string{"url", "download_url", "file_url"} {
		if value := stringFromAny(data[key]); normalizedFileURL(value) != "" {
			base.URL = strings.TrimSpace(value)
			base.LocalPath = ""
			return base
		}
	}
	for _, key := range []string{"file", "path", "file_path"} {
		value := strings.TrimSpace(strings.TrimPrefix(stringFromAny(data[key]), "file://"))
		if isSupportedLocalFile(value) {
			base.LocalPath = value
			base.URL = ""
			return base
		}
		if normalizedFileURL(value) != "" {
			base.URL = value
			base.LocalPath = ""
			return base
		}
	}
	return base
}

func fileRefFromOneBotHistory(base fileRef, data map[string]any) (fileRef, bool) {
	for _, key := range []string{"messages", "message", "segments", "data"} {
		if next, ok := fileRefFromOneBotValue(base, data[key]); ok {
			return next, true
		}
	}
	return base, false
}

func fileRefFromOneBotValue(base fileRef, value any) (fileRef, bool) {
	switch item := value.(type) {
	case []any:
		for i := len(item) - 1; i >= 0; i-- {
			if next, ok := fileRefFromOneBotValue(base, item[i]); ok {
				return next, true
			}
		}
	case []map[string]any:
		for i := len(item) - 1; i >= 0; i-- {
			if next, ok := fileRefFromOneBotValue(base, item[i]); ok {
				return next, true
			}
		}
	case map[string]any:
		if strings.EqualFold(strings.TrimSpace(stringFromAny(item["type"])), "file") {
			if segmentData, ok := item["data"].(map[string]any); ok {
				return fileRefFromOneBotSegment(base, segmentData)
			}
		}
		for _, key := range []string{"message", "segments", "data"} {
			if next, ok := fileRefFromOneBotValue(base, item[key]); ok {
				return next, true
			}
		}
	}
	return base, false
}

func fileRefFromOneBotSegment(base fileRef, data map[string]any) (fileRef, bool) {
	name := firstNonEmpty(stringFromAny(data["name"]), stringFromAny(data["filename"]), stringFromAny(data["file"]))
	fileID := firstNonEmpty(stringFromAny(data["file_id"]), stringFromAny(data["id"]), stringFromAny(data["fid"]))
	baseName := strings.TrimSpace(base.Name)
	if baseName != "" && name != "" && !strings.EqualFold(filepath.Base(name), filepath.Base(baseName)) {
		return base, false
	}
	if baseName == "" && strings.TrimSpace(base.FileID) != "" && fileID != "" && fileID != base.FileID {
		return base, false
	}
	if name != "" {
		base.Name = name
	}
	if fileID != "" {
		base.FileID = fileID
	}
	if busID := firstNonEmpty(stringFromAny(data["busid"]), stringFromAny(data["bus_id"])); busID != "" {
		base.BusID = busID
	}
	base = fileRefFromOneBotData(base, data)
	return base, true
}

// readURL 按大小限制读取远程文件内容。
func (p *FileParserPlugin) readURL(ctx context.Context, raw string) ([]byte, string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, raw, nil)
	if err != nil {
		return nil, "", err
	}
	req.Header.Set("User-Agent", "DianaQQBot/0.1")
	resp, err := p.client.Do(req)
	if err != nil {
		return nil, "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 400 {
		return nil, resp.Header.Get("Content-Type"), fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	// 多读 1 字节判断是否超限，避免把大文件完整读进内存。
	limited := io.LimitReader(resp.Body, p.maxBytes+1)
	data, err := io.ReadAll(limited)
	if err != nil {
		return nil, resp.Header.Get("Content-Type"), err
	}
	if int64(len(data)) > p.maxBytes {
		return nil, resp.Header.Get("Content-Type"), fmt.Errorf("file exceeds %d bytes", p.maxBytes)
	}
	return data, resp.Header.Get("Content-Type"), nil
}

func (p *FileParserPlugin) readLocal(localPath string) ([]byte, string, error) {
	localPath = strings.TrimSpace(strings.TrimPrefix(localPath, "file://"))
	if localPath == "" || !filepath.IsAbs(localPath) {
		return nil, "", fmt.Errorf("invalid local file path")
	}
	info, err := os.Stat(localPath)
	if err != nil {
		return nil, "", err
	}
	if info.IsDir() {
		return nil, "", fmt.Errorf("path is a directory")
	}
	if info.Size() > p.maxBytes {
		return nil, "", fmt.Errorf("file exceeds %d bytes", p.maxBytes)
	}
	data, err := os.ReadFile(localPath)
	if err != nil {
		return nil, "", err
	}
	return data, "", nil
}

var supportedFileExts = map[string]struct{}{
	".txt":      {},
	".pdf":      {},
	".md":       {},
	".markdown": {},
	".json":     {},
	".jsonl":    {},
	".csv":      {},
	".tsv":      {},
	".log":      {},
	".yaml":     {},
	".yml":      {},
	".toml":     {},
	".ini":      {},
	".conf":     {},
	".xml":      {},
	".html":     {},
	".htm":      {},
}

// isSupportedFileURL 判断 URL 是否是支持的文本类文件链接。
func isSupportedFileURL(raw string) bool {
	parsed := normalizedFileURL(raw)
	if parsed == "" {
		return false
	}
	return isSupportedFileName(parsed)
}

func normalizedFileURL(raw string) string {
	raw = strings.TrimSpace(raw)
	parsed, err := url.Parse(raw)
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") {
		return ""
	}
	return parsed.Path
}

func isSupportedLocalFile(localPath string) bool {
	localPath = strings.TrimSpace(strings.TrimPrefix(localPath, "file://"))
	return filepath.IsAbs(localPath) && isSupportedFileName(localPath)
}

func isSupportedFileName(name string) bool {
	_, ok := supportedFileExts[strings.ToLower(path.Ext(name))]
	return ok
}

func looksPDF(name string, contentType string, data []byte) bool {
	contentType = strings.ToLower(contentType)
	return strings.EqualFold(path.Ext(name), ".pdf") ||
		strings.Contains(contentType, "application/pdf") ||
		bytes.HasPrefix(bytes.TrimSpace(data), []byte("%PDF-"))
}

// looksTextual 根据扩展名、Content-Type 和内容判断文件是否像文本。
func looksTextual(name string, contentType string, data []byte) bool {
	contentType = strings.ToLower(contentType)
	if strings.Contains(contentType, "text/") ||
		strings.Contains(contentType, "json") ||
		strings.Contains(contentType, "xml") ||
		strings.Contains(contentType, "yaml") ||
		strings.Contains(contentType, "csv") {
		return true
	}
	if _, ok := supportedFileExts[strings.ToLower(path.Ext(name))]; ok {
		return true
	}
	// 没有可靠扩展名或 content-type 时，用 UTF-8 和 NUL 字节做最后的文本判断。
	if !utf8.Valid(data) {
		return false
	}
	return bytes.IndexByte(data, 0) < 0
}

func extractPDFText(data []byte, maxChars int) string {
	reader := bytes.NewReader(data)
	doc, err := pdf.NewReader(reader, int64(len(data)))
	if err != nil {
		return ""
	}
	var builder strings.Builder
	for pageNum := 1; pageNum <= doc.NumPage(); pageNum++ {
		if maxChars > 0 && len([]rune(builder.String())) >= maxChars {
			break
		}
		pageText := pdfPageText(doc.Page(pageNum))
		if strings.TrimSpace(pageText) == "" {
			continue
		}
		if builder.Len() > 0 {
			builder.WriteString("\n\n")
		}
		builder.WriteString(fmt.Sprintf("第 %d 页：\n", pageNum))
		builder.WriteString(pageText)
	}
	return builder.String()
}

func pdfNeedsOCR(data []byte, extracted string) bool {
	if strings.TrimSpace(extracted) == "" {
		return true
	}
	doc, err := pdf.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil || doc.NumPage() <= 0 {
		return true
	}
	threshold := doc.NumPage() * 16
	if threshold < 32 {
		threshold = 32
	}
	meaningful := 0
	for pageNum := 1; pageNum <= doc.NumPage() && meaningful < threshold; pageNum++ {
		for _, r := range pdfPageText(doc.Page(pageNum)) {
			if !unicode.IsSpace(r) {
				meaningful++
			}
		}
	}
	return meaningful < threshold
}

func pdfPageText(page pdf.Page) string {
	texts := append([]pdf.Text(nil), page.Content().Text...)
	if len(texts) == 0 {
		return ""
	}
	sort.Slice(texts, func(i, j int) bool {
		if texts[i].Y != texts[j].Y {
			return texts[i].Y > texts[j].Y
		}
		return texts[i].X < texts[j].X
	})
	type textLine struct {
		baseline float64
		items    []pdf.Text
	}
	lines := make([]textLine, 0, len(texts))
	for _, item := range texts {
		if len(lines) == 0 || math.Abs(item.Y-lines[len(lines)-1].baseline) > 2 {
			lines = append(lines, textLine{baseline: item.Y})
		}
		line := &lines[len(lines)-1]
		line.items = append(line.items, item)
	}
	var builder strings.Builder
	for _, line := range lines {
		sort.Slice(line.items, func(i, j int) bool { return line.items[i].X < line.items[j].X })
		text := joinPDFTextLine(line.items)
		if text == "" {
			continue
		}
		if builder.Len() > 0 {
			builder.WriteByte('\n')
		}
		builder.WriteString(text)
	}
	return strings.TrimSpace(builder.String())
}

func joinPDFTextLine(items []pdf.Text) string {
	var builder strings.Builder
	var previous pdf.Text
	hasPrevious := false
	pendingSpace := false
	for _, item := range items {
		raw := item.S
		text := strings.TrimSpace(raw)
		if text == "" {
			if strings.IndexFunc(raw, func(r rune) bool { return r == ' ' || r == '\t' }) >= 0 {
				pendingSpace = hasPrevious
			}
			continue
		}
		if hasPrevious && (pendingSpace || pdfTextGapNeedsSpace(previous, item)) {
			builder.WriteByte(' ')
		}
		builder.WriteString(text)
		previous = item
		hasPrevious = true
		pendingSpace = false
	}
	return strings.TrimSpace(builder.String())
}

func pdfTextGapNeedsSpace(previous, current pdf.Text) bool {
	gap := current.X - (previous.X + previous.W)
	fontSize := previous.FontSize
	if fontSize <= 0 || (current.FontSize > 0 && current.FontSize < fontSize) {
		fontSize = current.FontSize
	}
	threshold := 0.5
	if fontSize > 0 && fontSize*0.15 > threshold {
		threshold = fontSize * 0.15
	}
	return gap > threshold
}

// sanitizeFileText 清理并截断文件文本内容。
func sanitizeFileText(data []byte, maxChars int) string {
	return sanitizeFileTextString(string(data), maxChars)
}

func sanitizeFileTextString(text string, maxChars int) string {
	text = strings.ReplaceAll(text, "\r\n", "\n")
	text = strings.ReplaceAll(text, "\r", "\n")
	text = stripControlChars(text)
	text = strings.TrimSpace(text)
	runes := []rune(text)
	if maxChars > 0 && len(runes) > maxChars {
		// 按 rune 截断，避免中文文本被按字节截坏。
		text = string(runes[:maxChars]) + "\n...[已截断]"
	}
	return text
}

// stripControlChars 移除文件文本中的控制字符。
func stripControlChars(text string) string {
	return strings.Map(func(r rune) rune {
		switch r {
		case '\n', '\t':
			return r
		}
		if r < 32 {
			return -1
		}
		return r
	}, text)
}

// indentText 给多行文本加统一缩进。
func indentText(text string, prefix string) string {
	lines := strings.Split(text, "\n")
	for i, line := range lines {
		lines[i] = prefix + line
	}
	return strings.Join(lines, "\n")
}

// fileNameFromURL 从 URL 路径中提取文件名。
func fileNameFromURL(raw string) string {
	parsed, err := url.Parse(raw)
	if err != nil {
		return "文件"
	}
	name := path.Base(parsed.Path)
	if name == "." || name == "/" || name == "" {
		return "文件"
	}
	return name
}

// firstNonEmpty 返回第一个去空白后非空的字符串。
func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}
