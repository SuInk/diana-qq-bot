package qqbot

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"diana-qq-bot/model/llm"
)

func TestFileParserSchedulesOCRTaskForScannedPDF(t *testing.T) {
	plugin := NewFileParserPlugin(&http.Client{Transport: roundTripFunc(func(_ *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"application/pdf"}},
			Body:       io.NopCloser(strings.NewReader("%PDF-1.4\n%%EOF")),
		}, nil
	})})
	resp, err := plugin.Handle(context.Background(), PluginRequest{Text: "看看 https://example.com/scan.pdf"})
	if err != nil {
		t.Fatal(err)
	}
	if resp == nil || !resp.Handled || len(resp.Tasks) != 1 {
		t.Fatalf("resp = %#v", resp)
	}
	if resp.Tasks[0].Kind != "document_ocr" || resp.Tasks[0].Run == nil {
		t.Fatalf("task = %#v", resp.Tasks[0])
	}
}

func TestDocumentOCRTaskUsesParallelPageSubagentsAndFinalCall(t *testing.T) {
	t.Setenv("DIANA_OCR_CACHE_DIR", t.TempDir())
	t.Setenv("DIANA_OCR_CONCURRENCY", "3")
	t.Setenv("DIANA_OCR_RENDER_CONCURRENCY", "3")
	renderer := &fakePDFRenderer{pages: 3}
	document := scannedPDFDocument{Name: "scan.pdf", Data: []byte("unique scanned pdf")}
	task := newDocumentOCRTask(renderer, "这份文件讲了什么", []scannedPDFDocument{document}, "")

	var calls atomic.Int64
	var active atomic.Int64
	var maxActive atomic.Int64
	var finalPromptMu sync.Mutex
	var finalPrompt string
	services := PluginTaskServices{
		Generate: func(_ context.Context, req llm.GenerateRequest) (string, error) {
			calls.Add(1)
			if requestContainsImage(req) {
				current := active.Add(1)
				for {
					previous := maxActive.Load()
					if current <= previous || maxActive.CompareAndSwap(previous, current) {
						break
					}
				}
				time.Sleep(30 * time.Millisecond)
				active.Add(-1)
				return fmt.Sprintf("第 %d 页内容", calls.Load()), nil
			}
			finalPromptMu.Lock()
			finalPrompt = req.Messages[len(req.Messages)-1].Content
			finalPromptMu.Unlock()
			return "最终文档回答", nil
		},
		Report: func(PluginTaskProgress) {},
	}
	result, err := task.Run(context.Background(), services)
	if err != nil {
		t.Fatal(err)
	}
	if result.Reply != "最终文档回答" {
		t.Fatalf("reply = %q", result.Reply)
	}
	if calls.Load() != 4 {
		t.Fatalf("LLM calls = %d, want 4", calls.Load())
	}
	if maxActive.Load() < 2 {
		t.Fatalf("max concurrent OCR calls = %d, want at least 2", maxActive.Load())
	}
	finalPromptMu.Lock()
	defer finalPromptMu.Unlock()
	if !strings.Contains(finalPrompt, "第 1 页") || !strings.Contains(finalPrompt, "这份文件讲了什么") {
		t.Fatalf("final prompt = %q", finalPrompt)
	}
}

func TestReduceOCRContextFallsBackWhenOneSubagentFails(t *testing.T) {
	t.Setenv("DIANA_OCR_REDUCE_CHUNK_CHARS", "20")
	t.Setenv("DIANA_OCR_FINAL_CONTEXT_CHARS", "100")
	services := PluginTaskServices{
		Generate: func(_ context.Context, req llm.GenerateRequest) (string, error) {
			prompt := req.Messages[len(req.Messages)-1].Content
			if strings.Contains(prompt, "OCR 分块 1/") {
				return "", context.DeadlineExceeded
			}
			return "整理后的第二块", nil
		},
	}

	result, err := reduceOCRContext(context.Background(), services, "概述", strings.Repeat("第一块内容", 5)+"\n\n"+strings.Repeat("第二块内容", 5))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(result, "保留 OCR 原文") || !strings.Contains(result, "整理后的第二块") {
		t.Fatalf("result = %q", result)
	}
}

func TestWASMPDFRendererRendersImageOnlyPage(t *testing.T) {
	t.Setenv("DIANA_OCR_RENDER_CONCURRENCY", "1")
	renderer := newWASMPDFRenderer()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	session, err := renderer.Open(ctx, minimalImageOnlyPDF())
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()
	if session.PageCount() != 1 {
		t.Fatalf("page count = %d", session.PageCount())
	}
	jpeg, err := session.RenderJPEG(ctx, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(jpeg) < 4 || jpeg[0] != 0xff || jpeg[1] != 0xd8 {
		t.Fatalf("rendered data is not JPEG: %x", jpeg[:min(len(jpeg), 8)])
	}
}

func requestContainsImage(req llm.GenerateRequest) bool {
	for _, message := range req.Messages {
		for _, part := range message.Parts {
			if part.Type == llm.ContentPartImageURL {
				return true
			}
		}
	}
	return false
}

type fakePDFRenderer struct {
	pages int
}

func (r *fakePDFRenderer) Open(context.Context, []byte) (pdfRenderSession, error) {
	return &fakePDFRenderSession{pages: r.pages}, nil
}

type fakePDFRenderSession struct {
	pages int
}

func (s *fakePDFRenderSession) PageCount() int { return s.pages }
func (s *fakePDFRenderSession) Close() error   { return nil }
func (s *fakePDFRenderSession) RenderJPEG(context.Context, int) ([]byte, error) {
	return []byte{0xff, 0xd8, 0xff, 0xd9}, nil
}

func minimalImageOnlyPDF() []byte {
	objects := []string{
		"<< /Type /Catalog /Pages 2 0 R >>",
		"<< /Type /Pages /Kids [3 0 R] /Count 1 >>",
		"<< /Type /Page /Parent 2 0 R /MediaBox [0 0 100 100] /Resources << >> /Contents 4 0 R >>",
	}
	stream := "0.9 g\n0 0 100 100 re f\n"
	objects = append(objects, fmt.Sprintf("<< /Length %d >>\nstream\n%sendstream", len(stream), stream))

	var pdf bytes.Buffer
	pdf.WriteString("%PDF-1.4\n")
	offsets := make([]int, len(objects)+1)
	for index, object := range objects {
		offsets[index+1] = pdf.Len()
		fmt.Fprintf(&pdf, "%d 0 obj\n%s\nendobj\n", index+1, object)
	}
	xrefOffset := pdf.Len()
	fmt.Fprintf(&pdf, "xref\n0 %d\n", len(objects)+1)
	pdf.WriteString("0000000000 65535 f \n")
	for index := 1; index < len(offsets); index++ {
		fmt.Fprintf(&pdf, "%010d 00000 n \n", offsets[index])
	}
	fmt.Fprintf(&pdf, "trailer\n<< /Size %d /Root 1 0 R >>\nstartxref\n%d\n%%%%EOF\n", len(objects)+1, xrefOffset)
	return pdf.Bytes()
}
