package qqbot

import (
	"context"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestNativeMacPDFResultFormatting(t *testing.T) {
	result := nativeMacPDFResult{
		TotalPages:     3,
		ProcessedPages: 3,
		TextPages:      2,
		Pages: []nativeMacPDFPage{
			{Number: 1, Source: "pdfkit", Text: "第一页正文"},
			{Number: 2, Source: "pdfkit", Text: "第二页正文"},
		},
	}
	formatted := formatNativeMacPDFText(result, 1000)
	if !strings.Contains(formatted, "第 1 页：\n第一页正文") || !strings.Contains(formatted, "第 2 页：\n第二页正文") {
		t.Fatalf("formatted=%q", formatted)
	}
	document, ok := nativeMacDocumentOCRResult(scannedPDFDocument{Name: "test.pdf", Hash: "hash"}, result)
	if !ok || len(document.Pages) != 3 || document.Pages[0] != "第一页正文" || document.Pages[1] != "第二页正文" || document.Pages[2] != "" {
		t.Fatalf("document=%#v ok=%v", document, ok)
	}
}

func TestDecodeNativeMacPDFOutputIgnoresVisionDiagnostics(t *testing.T) {
	result, err := decodeNativeMacPDFOutput([]byte(`{"total_pages":1,"processed_pages":1,"text_pages":0,"vision_pages":1,"pages":[{"number":1,"source":"vision","text":"识别结果"}]}
Vision framework diagnostic output`))
	if err != nil {
		t.Fatal(err)
	}
	if result.VisionPages != 1 || len(result.Pages) != 1 || result.Pages[0].Text != "识别结果" {
		t.Fatalf("result=%#v", result)
	}
}

func TestFileParserPrefersMacPDFKitText(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("macOS helper integration")
	}
	helper := filepath.Join(t.TempDir(), "pdf-helper")
	script := `#!/bin/sh
printf '%s\n' '{"total_pages":2,"processed_pages":2,"text_pages":2,"vision_pages":0,"pages":[{"number":1,"source":"pdfkit","text":"这是第一页的原生文本内容，足够用于确认 PDFKit 分流正常。"},{"number":2,"source":"pdfkit","text":"这是第二页的原生文本内容，不应创建视觉 OCR 子任务。"}]}'
`
	if err := os.WriteFile(helper, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DIANA_MACOS_PDF_HELPER", helper)
	plugin := NewFileParserPlugin(&http.Client{Transport: roundTripFunc(func(_ *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"application/pdf"}},
			Body:       io.NopCloser(strings.NewReader("%PDF-1.4\n%%EOF")),
		}, nil
	})})
	resp, err := plugin.Handle(context.Background(), PluginRequest{Text: "看看 https://example.com/text.pdf"})
	if err != nil {
		t.Fatal(err)
	}
	if resp == nil || !resp.Handled || len(resp.Tasks) != 0 || !strings.Contains(resp.Context, "macOS PDFKit 文本层") || !strings.Contains(resp.Context, "第一页的原生文本") {
		t.Fatalf("resp=%#v", resp)
	}
}
