package qqbot

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"diana-qq-bot/model/llm"
)

const (
	defaultOCRMaxPagesPerFile = 48
	defaultOCRConcurrency     = 3
	defaultOCRCallTimeout     = 180 * time.Second
	defaultOCRReduceTimeout   = 5 * time.Minute
	defaultOCRFinalTimeout    = 5 * time.Minute
	defaultOCRTaskTimeout     = 60 * time.Minute
	defaultOCRPageMaxChars    = 12000
	defaultOCRFinalContext    = 60000
	defaultOCRReduceChunk     = 20000
	defaultOCRCacheVersion    = "vision-ocr-v1"
)

type scannedPDFDocument struct {
	Name   string
	Source string
	Data   []byte
	Hash   string
}

type documentOCRResult struct {
	Name           string   `json:"name"`
	Hash           string   `json:"hash"`
	TotalPages     int      `json:"total_pages"`
	ProcessedPages int      `json:"processed_pages"`
	Truncated      bool     `json:"truncated"`
	Pages          []string `json:"pages"`
}

type documentOCRCache struct {
	Version string            `json:"version"`
	SavedAt time.Time         `json:"saved_at"`
	Result  documentOCRResult `json:"result"`
}

func newDocumentOCRTask(renderer pdfPageRenderer, prompt string, documents []scannedPDFDocument, knownContext string) PluginTask {
	documents = append([]scannedPDFDocument(nil), documents...)
	for i := range documents {
		if documents[i].Hash == "" {
			hash := sha256.Sum256(documents[i].Data)
			documents[i].Hash = hex.EncodeToString(hash[:])
		}
	}
	name := documentTaskName(documents)
	return PluginTask{
		Kind:           "document_ocr",
		Name:           name,
		Key:            documentOCRTaskKey(prompt, documents),
		StartedMessage: fmt.Sprintf("已启动文档 OCR 子任务：%s。页面会并行识别，期间不会阻塞群聊", name),
		Timeout:        time.Duration(envInt("DIANA_OCR_TASK_TIMEOUT_MINUTES", int(defaultOCRTaskTimeout/time.Minute))) * time.Minute,
		Run: func(ctx context.Context, services PluginTaskServices) (PluginTaskResult, error) {
			return runDocumentOCRTask(ctx, services, renderer, prompt, documents, knownContext)
		},
	}
}

func documentTaskName(documents []scannedPDFDocument) string {
	names := make([]string, 0, len(documents))
	for _, document := range documents {
		if name := strings.TrimSpace(document.Name); name != "" {
			names = append(names, name)
		}
	}
	if len(names) == 0 {
		return "扫描 PDF"
	}
	if len(names) == 1 {
		return names[0]
	}
	return fmt.Sprintf("%s 等 %d 个文件", names[0], len(names))
}

func documentOCRTaskKey(prompt string, documents []scannedPDFDocument) string {
	hash := sha256.New()
	_, _ = hash.Write([]byte(defaultOCRCacheVersion))
	_, _ = hash.Write([]byte{0})
	_, _ = hash.Write([]byte(strings.TrimSpace(prompt)))
	for _, document := range documents {
		_, _ = hash.Write([]byte{0})
		_, _ = hash.Write([]byte(document.Hash))
	}
	return "document_ocr:" + hex.EncodeToString(hash.Sum(nil))
}

func runDocumentOCRTask(ctx context.Context, services PluginTaskServices, renderer pdfPageRenderer, prompt string, documents []scannedPDFDocument, knownContext string) (PluginTaskResult, error) {
	if services.Generate == nil {
		return PluginTaskResult{}, fmt.Errorf("document OCR: LLM subagent service is unavailable")
	}
	if renderer == nil {
		renderer = newWASMPDFRenderer()
	}
	if services.Report != nil {
		services.Report(PluginTaskProgress{Phase: "prepare", Message: "正在检查 OCR 缓存和 PDF 页数"})
	}

	results := make([]documentOCRResult, 0, len(documents))
	for index, document := range documents {
		if cached, ok := loadDocumentOCRCache(document.Hash); ok && documentOCRCacheCoversCurrentLimit(cached) {
			cached.Name = firstNonEmpty(document.Name, cached.Name)
			results = append(results, cached)
			continue
		}
		if nativeMacPDFHelperPath() != "" {
			if services.Report != nil {
				services.Report(PluginTaskProgress{Phase: "ocr", Message: fmt.Sprintf("正在使用 macOS PDFKit/Vision 本地识别《%s》", document.Name)})
			}
			nativeResult, nativeAvailable, nativeErr := runNativeMacPDF(
				ctx,
				document.Data,
				"ocr",
				envInt("DIANA_OCR_MAX_PAGES_PER_FILE", defaultOCRMaxPagesPerFile),
				envInt("DIANA_OCR_PAGE_MAX_CHARS", defaultOCRPageMaxChars),
			)
			if nativeErr != nil && ctx.Err() != nil {
				return PluginTaskResult{}, ctx.Err()
			}
			if nativeAvailable && nativeErr == nil {
				if result, ok := nativeMacDocumentOCRResult(document, nativeResult); ok {
					results = append(results, result)
					_ = saveDocumentOCRCache(result)
					if services.Report != nil {
						services.Report(PluginTaskProgress{
							Phase:     "ocr",
							Message:   fmt.Sprintf("macOS 本地识别《%s》已完成：%d/%d 页", document.Name, result.ProcessedPages, result.TotalPages),
							Completed: result.ProcessedPages,
							Total:     result.ProcessedPages,
						})
					}
					continue
				}
			}
		}
		result, err := ocrPDFDocument(ctx, services, renderer, document, index, len(documents))
		if err != nil {
			return PluginTaskResult{}, err
		}
		results = append(results, result)
		_ = saveDocumentOCRCache(result)
	}

	ocrContext := formatDocumentOCRResults(results)
	fullContext := strings.TrimSpace(strings.Join(nonEmptyStrings([]string{knownContext, ocrContext}), "\n\n"))
	if fullContext == "" {
		return PluginTaskResult{}, fmt.Errorf("document OCR did not produce text")
	}
	if len([]rune(fullContext)) > envInt("DIANA_OCR_FINAL_CONTEXT_CHARS", defaultOCRFinalContext) {
		if services.Report != nil {
			services.Report(PluginTaskProgress{Phase: "reduce", Message: "识别结果较长，正在由多个子代理并行整理重点"})
		}
		reduced, err := reduceOCRContext(ctx, services, prompt, fullContext)
		if err != nil {
			return PluginTaskResult{}, err
		}
		fullContext = reduced
	}

	if services.Report != nil {
		services.Report(PluginTaskProgress{Phase: "answer", Message: "OCR 已完成，正在生成最终回复"})
	}
	question := strings.TrimSpace(prompt)
	if question == "" {
		question = "请概述这些文件的主要内容，并指出重要结论。"
	}
	callCtx, cancel := context.WithTimeout(ctx, defaultOCRFinalTimeout)
	defer cancel()
	reply, err := services.Generate(callCtx, llm.GenerateRequest{Messages: []llm.Message{
		{
			Role: llm.RoleSystem,
			Content: strings.TrimSpace(`你是 Diana 的文档分析子代理。请根据已经完成的 OCR 结果回答当前 QQ 消息。

要求：
- 只回答当前问题；上下文只作参考，不要转而回复旧消息。
- 不要声称无法读取文件，也不要虚构 OCR 中不存在的内容。
- 涉及差异或结论时尽量标注文件名和页码。
- QQ 默认不启用 Markdown，请使用自然的纯文本，避免标题符号、表格语法和过度分条。
- 内容较长时先给结论，再给必要依据。`),
		},
		{
			Role:    llm.RoleUser,
			Content: fmt.Sprintf("当前问题：\n%s\n\n文档 OCR 结果：\n%s", question, fullContext),
		},
	}})
	if err != nil {
		return PluginTaskResult{}, fmt.Errorf("document OCR final answer: %w", err)
	}
	return PluginTaskResult{Reply: reply}, nil
}

func ocrPDFDocument(ctx context.Context, services PluginTaskServices, renderer pdfPageRenderer, document scannedPDFDocument, documentIndex int, documentTotal int) (documentOCRResult, error) {
	firstSession, err := renderer.Open(ctx, document.Data)
	if err != nil {
		return documentOCRResult{}, fmt.Errorf("OCR %s: %w", document.Name, err)
	}
	totalPages := firstSession.PageCount()
	pageLimit := envInt("DIANA_OCR_MAX_PAGES_PER_FILE", defaultOCRMaxPagesPerFile)
	processedPages := totalPages
	if processedPages > pageLimit {
		processedPages = pageLimit
	}
	if processedPages <= 0 {
		_ = firstSession.Close()
		return documentOCRResult{}, fmt.Errorf("OCR %s: PDF has no pages", document.Name)
	}

	workers := envInt("DIANA_OCR_CONCURRENCY", defaultOCRConcurrency)
	if renderWorkers := envInt("DIANA_OCR_RENDER_CONCURRENCY", defaultOCRRenderConcurrency); workers > renderWorkers {
		workers = renderWorkers
	}
	if workers > processedPages {
		workers = processedPages
	}
	if workers > maxSubagentLLMConcurrency {
		workers = maxSubagentLLMConcurrency
	}
	sessions := []pdfRenderSession{firstSession}
	for len(sessions) < workers {
		session, openErr := renderer.Open(ctx, document.Data)
		if openErr != nil {
			for _, opened := range sessions {
				_ = opened.Close()
			}
			return documentOCRResult{}, fmt.Errorf("OCR %s: open render worker: %w", document.Name, openErr)
		}
		sessions = append(sessions, session)
	}
	defer func() {
		for _, session := range sessions {
			_ = session.Close()
		}
	}()

	workCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	pages := make([]string, processedPages)
	jobs := make(chan int)
	var completed atomic.Int64
	var firstErr error
	var errOnce sync.Once
	var wg sync.WaitGroup

	for _, session := range sessions {
		session := session
		wg.Add(1)
		go func() {
			defer wg.Done()
			for pageIndex := range jobs {
				if workCtx.Err() != nil {
					return
				}
				imageData, renderErr := session.RenderJPEG(workCtx, pageIndex)
				if renderErr != nil {
					errOnce.Do(func() { firstErr = renderErr; cancel() })
					return
				}
				pageText, ocrErr := runPageVisionOCR(workCtx, services, document.Name, pageIndex+1, totalPages, imageData)
				if ocrErr != nil {
					errOnce.Do(func() { firstErr = ocrErr; cancel() })
					return
				}
				pages[pageIndex] = pageText
				done := int(completed.Add(1))
				if services.Report != nil && shouldReportOCRProgress(done, processedPages) {
					message := fmt.Sprintf("正在识别《%s》：%d/%d 页", document.Name, done, processedPages)
					if documentTotal > 1 {
						message = fmt.Sprintf("正在识别第 %d/%d 个文件《%s》：%d/%d 页", documentIndex+1, documentTotal, document.Name, done, processedPages)
					}
					services.Report(PluginTaskProgress{Phase: "ocr", Message: message, Completed: done, Total: processedPages})
				}
			}
		}()
	}

	go func() {
		defer close(jobs)
		for pageIndex := 0; pageIndex < processedPages; pageIndex++ {
			select {
			case jobs <- pageIndex:
			case <-workCtx.Done():
				return
			}
		}
	}()
	wg.Wait()
	if firstErr != nil {
		return documentOCRResult{}, fmt.Errorf("OCR %s: %w", document.Name, firstErr)
	}
	if err := workCtx.Err(); err != nil && ctx.Err() != nil {
		return documentOCRResult{}, ctx.Err()
	}
	return documentOCRResult{
		Name:           document.Name,
		Hash:           document.Hash,
		TotalPages:     totalPages,
		ProcessedPages: processedPages,
		Truncated:      processedPages < totalPages,
		Pages:          pages,
	}, nil
}

func runPageVisionOCR(ctx context.Context, services PluginTaskServices, name string, page int, totalPages int, imageData []byte) (string, error) {
	callCtx, cancel := context.WithTimeout(ctx, defaultOCRCallTimeout)
	defer cancel()
	prompt := fmt.Sprintf("文档《%s》第 %d/%d 页。请完整转写本页。", name, page, totalPages)
	text, err := services.Generate(callCtx, llm.GenerateRequest{Messages: []llm.Message{
		{
			Role: llm.RoleSystem,
			Content: strings.TrimSpace(`你是高精度中文文档 OCR 子代理。严格转写页面中的可辨文字，保持自然阅读顺序。

要求：
- 不要总结、解释、回答文档内容，也不要补写页面上没有的信息。
- 标题、正文、脚注、页码都应尽量保留。
- 表格按行转成清晰纯文本；公式使用可读的纯文本表达。
- 不使用 Markdown 代码块。页面无可辨文字时只返回“[无可辨文字]”。`),
		},
		{
			Role:    llm.RoleUser,
			Content: prompt,
			Parts: []llm.ContentPart{
				{Type: llm.ContentPartText, Text: prompt},
				{Type: llm.ContentPartImageURL, ImageURL: imageBytesAsDataURL(imageData, "image/jpeg"), Detail: "high"},
			},
		},
	}})
	if err != nil {
		return "", fmt.Errorf("page %d vision OCR: %w", page, err)
	}
	text = sanitizeFileTextString(text, defaultOCRPageMaxChars)
	if text == "" {
		text = "[无可辨文字]"
	}
	return text, nil
}

func shouldReportOCRProgress(completed int, total int) bool {
	if completed <= 0 || total <= 0 {
		return false
	}
	if completed == 1 || completed == total {
		return true
	}
	step := total / 4
	if step < 1 {
		step = 1
	}
	return completed%step == 0
}

func formatDocumentOCRResults(results []documentOCRResult) string {
	var builder strings.Builder
	for index, result := range results {
		if index > 0 {
			builder.WriteString("\n\n")
		}
		builder.WriteString(fmt.Sprintf("文件：《%s》（共 %d 页", result.Name, result.TotalPages))
		if result.Truncated {
			builder.WriteString(fmt.Sprintf("，仅处理前 %d 页", result.ProcessedPages))
		}
		builder.WriteString("）")
		for pageIndex, pageText := range result.Pages {
			builder.WriteString(fmt.Sprintf("\n\n第 %d 页：\n%s", pageIndex+1, strings.TrimSpace(pageText)))
		}
	}
	return strings.TrimSpace(builder.String())
}

func reduceOCRContext(ctx context.Context, services PluginTaskServices, prompt string, text string) (string, error) {
	chunks := splitOCRContext(text, envInt("DIANA_OCR_REDUCE_CHUNK_CHARS", defaultOCRReduceChunk))
	if len(chunks) <= 1 {
		return sanitizeFileTextString(text, envInt("DIANA_OCR_FINAL_CONTEXT_CHARS", defaultOCRFinalContext)), nil
	}
	results := make([]string, len(chunks))
	jobs := make(chan int)
	workers := envInt("DIANA_OCR_CONCURRENCY", defaultOCRConcurrency)
	if workers > len(chunks) {
		workers = len(chunks)
	}
	if workers > maxSubagentLLMConcurrency {
		workers = maxSubagentLLMConcurrency
	}
	var wg sync.WaitGroup
	var completed atomic.Int64
	for worker := 0; worker < workers; worker++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for index := range jobs {
				callCtx, callCancel := context.WithTimeout(ctx, defaultOCRReduceTimeout)
				result, err := services.Generate(callCtx, llm.GenerateRequest{Messages: []llm.Message{
					{
						Role:    llm.RoleSystem,
						Content: "你是文档整理子代理。围绕当前问题整理这段 OCR，保留文件名、页码、关键事实、数字、观点与差异。不要回答最终问题，不要虚构，使用紧凑纯文本。",
					},
					{
						Role:    llm.RoleUser,
						Content: fmt.Sprintf("当前问题：%s\n\nOCR 分块 %d/%d：\n%s", firstNonEmpty(strings.TrimSpace(prompt), "概述文档"), index+1, len(chunks), chunks[index]),
					},
				}})
				callCancel()
				if err != nil {
					if ctx.Err() != nil {
						return
					}
					results[index] = fallbackOCRReductionChunk(chunks[index], index, len(chunks))
					if services.Report != nil {
						services.Report(PluginTaskProgress{
							Phase:   "reduce",
							Message: fmt.Sprintf("第 %d/%d 个整理子代理超时，已保留该分块 OCR 原文继续处理", index+1, len(chunks)),
						})
					}
				} else {
					results[index] = strings.TrimSpace(result)
				}
				done := int(completed.Add(1))
				if services.Report != nil && shouldReportOCRProgress(done, len(chunks)) {
					services.Report(PluginTaskProgress{Phase: "reduce", Message: fmt.Sprintf("文档整理子代理已完成 %d/%d 个分块", done, len(chunks)), Completed: done, Total: len(chunks)})
				}
			}
		}()
	}
	go func() {
		defer close(jobs)
		for index := range chunks {
			select {
			case jobs <- index:
			case <-ctx.Done():
				return
			}
		}
	}()
	wg.Wait()
	if err := ctx.Err(); err != nil {
		return "", fmt.Errorf("reduce OCR context: %w", err)
	}
	return sanitizeFileTextString(strings.Join(nonEmptyStrings(results), "\n\n"), envInt("DIANA_OCR_FINAL_CONTEXT_CHARS", defaultOCRFinalContext)), nil
}

func fallbackOCRReductionChunk(chunk string, index int, total int) string {
	limit := envInt("DIANA_OCR_FINAL_CONTEXT_CHARS", defaultOCRFinalContext)
	if total > 0 {
		limit /= total
	}
	if limit < 1000 {
		limit = 1000
	}
	text := sanitizeFileTextString(chunk, limit)
	return fmt.Sprintf("[第 %d/%d 个分块未完成整理，以下保留 OCR 原文]\n%s", index+1, total, text)
}

func splitOCRContext(text string, maxChars int) []string {
	if maxChars <= 0 {
		maxChars = defaultOCRReduceChunk
	}
	paragraphs := strings.Split(strings.TrimSpace(text), "\n\n")
	chunks := make([]string, 0, len(paragraphs))
	var builder strings.Builder
	flush := func() {
		if value := strings.TrimSpace(builder.String()); value != "" {
			chunks = append(chunks, value)
		}
		builder.Reset()
	}
	for _, paragraph := range paragraphs {
		paragraph = strings.TrimSpace(paragraph)
		if paragraph == "" {
			continue
		}
		if builder.Len() > 0 && len([]rune(builder.String()))+len([]rune(paragraph))+2 > maxChars {
			flush()
		}
		runes := []rune(paragraph)
		for len(runes) > maxChars {
			if builder.Len() > 0 {
				flush()
			}
			chunks = append(chunks, string(runes[:maxChars]))
			runes = runes[maxChars:]
		}
		if len(runes) > 0 {
			if builder.Len() > 0 {
				builder.WriteString("\n\n")
			}
			builder.WriteString(string(runes))
		}
	}
	flush()
	return chunks
}

func loadDocumentOCRCache(hash string) (documentOCRResult, bool) {
	path, err := documentOCRCachePath(hash)
	if err != nil {
		return documentOCRResult{}, false
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return documentOCRResult{}, false
	}
	var cached documentOCRCache
	if json.Unmarshal(data, &cached) != nil || cached.Version != documentOCRCacheVersion() || len(cached.Result.Pages) == 0 {
		return documentOCRResult{}, false
	}
	return cached.Result, true
}

func documentOCRCacheCoversCurrentLimit(result documentOCRResult) bool {
	required := result.TotalPages
	if limit := envInt("DIANA_OCR_MAX_PAGES_PER_FILE", defaultOCRMaxPagesPerFile); required > limit {
		required = limit
	}
	return required > 0 && result.ProcessedPages >= required && len(result.Pages) >= required
}

func saveDocumentOCRCache(result documentOCRResult) error {
	path, err := documentOCRCachePath(result.Hash)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	data, err := json.Marshal(documentOCRCache{Version: documentOCRCacheVersion(), SavedAt: time.Now(), Result: result})
	if err != nil {
		return err
	}
	temp, err := os.CreateTemp(filepath.Dir(path), ".ocr-*.json")
	if err != nil {
		return err
	}
	tempPath := temp.Name()
	defer os.Remove(tempPath)
	if err := temp.Chmod(0o600); err != nil {
		_ = temp.Close()
		return err
	}
	if _, err := temp.Write(data); err != nil {
		_ = temp.Close()
		return err
	}
	if err := temp.Close(); err != nil {
		return err
	}
	return os.Rename(tempPath, path)
}

func documentOCRCachePath(hash string) (string, error) {
	dir, err := documentOCRCacheDir()
	if err != nil {
		return "", err
	}
	hash = strings.TrimSpace(hash)
	if len(hash) < 16 {
		return "", fmt.Errorf("invalid OCR cache hash")
	}
	return filepath.Join(dir, hash+".json"), nil
}

func documentOCRCacheDir() (string, error) {
	if value := strings.TrimSpace(os.Getenv("DIANA_OCR_CACHE_DIR")); value != "" {
		return value, nil
	}
	if dbPath := strings.TrimSpace(os.Getenv("APP_DB_PATH")); dbPath != "" {
		return filepath.Join(filepath.Dir(dbPath), "ocr-cache"), nil
	}
	cacheDir, err := os.UserCacheDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(cacheDir, "diana-qq-bot", "ocr-cache"), nil
}

func documentOCRCacheVersion() string {
	return firstNonEmpty(strings.TrimSpace(os.Getenv("DIANA_OCR_CACHE_VERSION")), defaultOCRCacheVersion)
}
