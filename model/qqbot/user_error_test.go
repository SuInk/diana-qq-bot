package qqbot

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func TestPublicQQErrorMessageHidesRelayURL(t *testing.T) {
	err := errors.New(`Post "https://relay.private.example/v1/responses": context deadline exceeded (Client.Timeout exceeded while awaiting headers)`)
	got := publicQQErrorMessage(err)
	if got != "模型服务响应超时，请稍后重试。" {
		t.Fatalf("message = %q", got)
	}
	for _, leaked := range []string{"relay.private.example", "/v1/responses", "https://"} {
		if strings.Contains(got, leaked) {
			t.Fatalf("message leaked %q: %q", leaked, got)
		}
	}
}

func TestPublicQQErrorMessageDoesNotExposeUnknownProviderError(t *testing.T) {
	err := errors.New(`request to https://relay.private.example/v1 failed: Authorization: Bearer example-secret-token`)
	got := publicQQErrorMessage(err)
	if got != "请求处理失败，请稍后重试。" {
		t.Fatalf("message = %q", got)
	}
	for _, leaked := range []string{"relay.private.example", "/v1", "example-secret-token", "Authorization", "request to"} {
		if strings.Contains(got, leaked) {
			t.Fatalf("message leaked %q: %q", leaked, got)
		}
	}
}

func TestPublicQQErrorMessageMapsEmptyModelOutput(t *testing.T) {
	err := errors.New("llm: openai-compatible chat completions output is empty")
	got := publicQQErrorMessage(err)
	if got != "模型服务暂时没有返回有效内容，请稍后重试。" {
		t.Fatalf("message = %q", got)
	}
	if strings.Contains(strings.ToLower(got), "openai") || strings.Contains(strings.ToLower(got), "output is empty") {
		t.Fatalf("message exposed provider details: %q", got)
	}
}

func TestReplyAndRecordSendsSanitizedErrorButKeepsDiagnostic(t *testing.T) {
	channel := &recordingChannel{}
	rawErr := errors.New(`Post "https://relay.private.example/v1/responses": context deadline exceeded (Client.Timeout exceeded while awaiting headers)`)
	runtime := NewRuntime(BotConfig{}, channel, NewPluginManager(), nil, nil, nil, func() (LLMProvider, error) {
		return failingLLMProvider{err: rawErr}, nil
	})
	event := MessageEvent{Kind: EventKindPrivate, UserID: "user", MessageID: "redacted-error"}
	outcome, err := runtime.replyAndRecord(context.Background(), event, "测试", "replied")
	if err != nil {
		t.Fatal(err)
	}
	if outcome != "error_replied" || len(channel.sent) != 1 {
		t.Fatalf("outcome=%q sent=%#v", outcome, channel.sent)
	}
	if got := channel.sent[0].Text; got != "出错了：模型服务响应超时，请稍后重试。" {
		t.Fatalf("sent text = %q", got)
	}
	if !strings.Contains(runtime.Status().LastError, "relay.private.example") {
		t.Fatalf("diagnostic error was unexpectedly redacted: %q", runtime.Status().LastError)
	}
}
