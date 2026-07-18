package qqbot

import (
	"context"
	"fmt"
	"io"
	"sync"

	pdfium "github.com/klippa-app/go-pdfium"
	"github.com/klippa-app/go-pdfium/references"
	"github.com/klippa-app/go-pdfium/requests"
	"github.com/klippa-app/go-pdfium/webassembly"
	"github.com/tetratelabs/wazero"
)

const (
	defaultOCRRenderConcurrency = 3
	defaultOCRRenderDPI         = 180
	defaultOCRJPEGQuality       = 82
	defaultOCRMaxImageBytes     = 4 << 20
)

type pdfPageRenderer interface {
	Open(context.Context, []byte) (pdfRenderSession, error)
}

type pdfRenderSession interface {
	PageCount() int
	RenderJPEG(context.Context, int) ([]byte, error)
	Close() error
}

type wasmPDFRenderer struct {
	once    sync.Once
	pool    pdfium.Pool
	initErr error
}

func newWASMPDFRenderer() pdfPageRenderer {
	return &wasmPDFRenderer{}
}

func (r *wasmPDFRenderer) init() {
	concurrency := boundedInt(envInt("DIANA_OCR_RENDER_CONCURRENCY", defaultOCRRenderConcurrency), 1, maxSubagentLLMConcurrency)
	// The embedded PDFium WASM receives PDF bytes directly and gets no host
	// filesystem mounts. This keeps rendering portable and isolates malformed PDFs.
	r.pool, r.initErr = webassembly.Init(webassembly.Config{
		MinIdle:      0,
		MaxIdle:      concurrency,
		MaxTotal:     concurrency,
		ReuseWorkers: true,
		FSConfig:     wazero.NewFSConfig(),
		Stdout:       io.Discard,
		Stderr:       io.Discard,
	})
}

func (r *wasmPDFRenderer) Open(ctx context.Context, data []byte) (pdfRenderSession, error) {
	if len(data) == 0 {
		return nil, fmt.Errorf("pdf renderer: empty document")
	}
	r.once.Do(r.init)
	if r.initErr != nil {
		return nil, fmt.Errorf("pdf renderer init: %w", r.initErr)
	}
	instance, err := r.pool.GetInstanceWithContext(ctx)
	if err != nil {
		return nil, fmt.Errorf("pdf renderer worker: %w", err)
	}
	document, err := callPDFiumWithContext(ctx, instance, func() (*references.FPDF_DOCUMENT, error) {
		opened, openErr := instance.OpenDocument(&requests.OpenDocument{File: &data})
		if openErr != nil {
			return nil, openErr
		}
		return &opened.Document, nil
	})
	if err != nil {
		_ = instance.Close()
		return nil, fmt.Errorf("open pdf: %w", err)
	}
	count, err := callPDFiumWithContext(ctx, instance, func() (int, error) {
		resp, countErr := instance.FPDF_GetPageCount(&requests.FPDF_GetPageCount{Document: *document})
		if countErr != nil {
			return 0, countErr
		}
		return resp.PageCount, nil
	})
	if err != nil || count <= 0 {
		_, _ = instance.FPDF_CloseDocument(&requests.FPDF_CloseDocument{Document: *document})
		_ = instance.Close()
		if err != nil {
			return nil, fmt.Errorf("pdf page count: %w", err)
		}
		return nil, fmt.Errorf("pdf renderer: document has no pages")
	}
	return &wasmPDFRenderSession{instance: instance, document: *document, pages: count}, nil
}

type wasmPDFRenderSession struct {
	instance pdfium.Pdfium
	document references.FPDF_DOCUMENT
	pages    int
	close    sync.Once
	closeErr error
}

func (s *wasmPDFRenderSession) PageCount() int {
	return s.pages
}

func (s *wasmPDFRenderSession) RenderJPEG(ctx context.Context, page int) ([]byte, error) {
	if page < 0 || page >= s.pages {
		return nil, fmt.Errorf("pdf renderer: page %d is out of range", page+1)
	}
	dpi := boundedInt(envInt("DIANA_OCR_RENDER_DPI", defaultOCRRenderDPI), 96, 300)
	quality := boundedInt(envInt("DIANA_OCR_JPEG_QUALITY", defaultOCRJPEGQuality), 40, 95)
	maxImageMB := boundedInt(envInt("DIANA_OCR_MAX_IMAGE_MB", defaultOCRMaxImageBytes>>20), 1, maxLLMImageBytes>>20)
	data, err := callPDFiumWithContext(ctx, s.instance, func() ([]byte, error) {
		resp, renderErr := s.instance.RenderToFile(&requests.RenderToFile{
			RenderPageInDPI: &requests.RenderPageInDPI{
				Page: requests.Page{ByIndex: &requests.PageByIndex{Document: s.document, Index: page}},
				DPI:  dpi,
			},
			OutputFormat:  requests.RenderToFileOutputFormatJPG,
			OutputTarget:  requests.RenderToFileOutputTargetBytes,
			OutputQuality: quality,
			MaxFileSize:   int64(maxImageMB) << 20,
		})
		if renderErr != nil {
			return nil, renderErr
		}
		if resp.ImageBytes == nil || len(*resp.ImageBytes) == 0 {
			return nil, fmt.Errorf("pdf renderer returned an empty image")
		}
		return append([]byte(nil), (*resp.ImageBytes)...), nil
	})
	if err != nil {
		return nil, fmt.Errorf("render page %d: %w", page+1, err)
	}
	return data, nil
}

func boundedInt(value int, minimum int, maximum int) int {
	if value < minimum {
		return minimum
	}
	if value > maximum {
		return maximum
	}
	return value
}

func (s *wasmPDFRenderSession) Close() error {
	s.close.Do(func() {
		if s.instance == nil {
			return
		}
		_, docErr := s.instance.FPDF_CloseDocument(&requests.FPDF_CloseDocument{Document: s.document})
		instanceErr := s.instance.Close()
		if docErr != nil {
			s.closeErr = docErr
		} else {
			s.closeErr = instanceErr
		}
	})
	return s.closeErr
}

func callPDFiumWithContext[T any](ctx context.Context, instance pdfium.Pdfium, call func() (T, error)) (T, error) {
	var zero T
	if err := ctx.Err(); err != nil {
		return zero, err
	}
	done := make(chan struct{})
	go func() {
		select {
		case <-ctx.Done():
			_ = instance.Kill()
		case <-done:
		}
	}()
	result, err := call()
	close(done)
	if ctxErr := ctx.Err(); ctxErr != nil {
		return zero, ctxErr
	}
	return result, err
}
