package qqbot

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"unicode"
)

const macPDFVisionHelperName = "diana-pdf-vision"

type nativeMacPDFPage struct {
	Number int    `json:"number"`
	Source string `json:"source"`
	Text   string `json:"text"`
}

type nativeMacPDFResult struct {
	TotalPages     int                `json:"total_pages"`
	ProcessedPages int                `json:"processed_pages"`
	TextPages      int                `json:"text_pages"`
	VisionPages    int                `json:"vision_pages"`
	Pages          []nativeMacPDFPage `json:"pages"`
}

func runNativeMacPDF(ctx context.Context, data []byte, mode string, maxPages int, pageMaxChars int) (nativeMacPDFResult, bool, error) {
	helper := nativeMacPDFHelperPath()
	if helper == "" {
		return nativeMacPDFResult{}, false, nil
	}
	if maxPages <= 0 {
		maxPages = defaultOCRMaxPagesPerFile
	}
	if pageMaxChars <= 0 {
		pageMaxChars = defaultOCRPageMaxChars
	}
	temp, err := os.CreateTemp("", "diana-pdf-*.pdf")
	if err != nil {
		return nativeMacPDFResult{}, true, fmt.Errorf("create native PDF input: %w", err)
	}
	path := temp.Name()
	defer func() { _ = os.Remove(path) }()
	if _, err := temp.Write(data); err != nil {
		_ = temp.Close()
		return nativeMacPDFResult{}, true, fmt.Errorf("write native PDF input: %w", err)
	}
	if err := temp.Close(); err != nil {
		return nativeMacPDFResult{}, true, fmt.Errorf("close native PDF input: %w", err)
	}

	command := exec.CommandContext(ctx, helper,
		"--mode", mode,
		"--max-pages", strconv.Itoa(maxPages),
		"--page-max-chars", strconv.Itoa(pageMaxChars),
		path,
	)
	var stderr bytes.Buffer
	command.Stderr = &stderr
	output, err := command.Output()
	if err != nil {
		if ctx.Err() != nil {
			return nativeMacPDFResult{}, true, ctx.Err()
		}
		return nativeMacPDFResult{}, true, fmt.Errorf("macOS PDF helper: %w: %s", err, strings.TrimSpace(stderr.String()))
	}
	result, err := decodeNativeMacPDFOutput(output)
	if err != nil {
		return nativeMacPDFResult{}, true, fmt.Errorf("decode macOS PDF helper output: %w", err)
	}
	if result.TotalPages < 0 || result.ProcessedPages < 0 || result.ProcessedPages > result.TotalPages {
		return nativeMacPDFResult{}, true, fmt.Errorf("macOS PDF helper returned invalid page counts")
	}
	sort.Slice(result.Pages, func(i, j int) bool { return result.Pages[i].Number < result.Pages[j].Number })
	return result, true, nil
}

func decodeNativeMacPDFOutput(output []byte) (nativeMacPDFResult, error) {
	var result nativeMacPDFResult
	if err := json.NewDecoder(bytes.NewReader(output)).Decode(&result); err != nil {
		return nativeMacPDFResult{}, err
	}
	return result, nil
}

func nativeMacPDFHelperPath() string {
	if runtime.GOOS != "darwin" {
		return ""
	}
	candidates := make([]string, 0, 3)
	if configured := strings.TrimSpace(os.Getenv("DIANA_MACOS_PDF_HELPER")); configured != "" {
		candidates = append(candidates, configured)
	}
	if executable, err := os.Executable(); err == nil {
		directory := filepath.Dir(executable)
		candidates = append(candidates,
			filepath.Join(directory, macPDFVisionHelperName),
			filepath.Clean(filepath.Join(directory, "..", "Resources", macPDFVisionHelperName)),
		)
	}
	for _, candidate := range candidates {
		info, err := os.Stat(candidate)
		if err == nil && !info.IsDir() && info.Mode()&0o111 != 0 {
			return candidate
		}
	}
	return ""
}

func nativeMacPDFHasText(result nativeMacPDFResult) bool {
	threshold := result.ProcessedPages * 16
	if threshold < 32 {
		threshold = 32
	}
	return nativeMacPDFMeaningfulChars(result) >= threshold
}

func nativeMacPDFMeaningfulChars(result nativeMacPDFResult) int {
	count := 0
	for _, page := range result.Pages {
		for _, value := range page.Text {
			if !unicode.IsSpace(value) {
				count++
			}
		}
	}
	return count
}

func formatNativeMacPDFText(result nativeMacPDFResult, maxChars int) string {
	var builder strings.Builder
	for _, page := range result.Pages {
		text := strings.TrimSpace(page.Text)
		if page.Number <= 0 || text == "" {
			continue
		}
		if builder.Len() > 0 {
			builder.WriteString("\n\n")
		}
		builder.WriteString(fmt.Sprintf("第 %d 页：\n%s", page.Number, text))
	}
	return sanitizeFileTextString(builder.String(), maxChars)
}

func nativeMacDocumentOCRResult(document scannedPDFDocument, result nativeMacPDFResult) (documentOCRResult, bool) {
	if result.ProcessedPages <= 0 || nativeMacPDFMeaningfulChars(result) == 0 {
		return documentOCRResult{}, false
	}
	pages := make([]string, result.ProcessedPages)
	for _, page := range result.Pages {
		if page.Number <= 0 || page.Number > len(pages) {
			continue
		}
		pages[page.Number-1] = sanitizeFileTextString(page.Text, envInt("DIANA_OCR_PAGE_MAX_CHARS", defaultOCRPageMaxChars))
	}
	return documentOCRResult{
		Name:           document.Name,
		Hash:           document.Hash,
		TotalPages:     result.TotalPages,
		ProcessedPages: result.ProcessedPages,
		Truncated:      result.ProcessedPages < result.TotalPages,
		Pages:          pages,
	}, true
}
