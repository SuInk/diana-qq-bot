package qqbot

import (
	"context"
	"errors"
	"strings"
)

// publicQQErrorMessage keeps operational details in logs while returning only
// a safe, useful summary to QQ users.
func publicQQErrorMessage(err error) string {
	if err == nil {
		return "请求处理失败，请稍后重试。"
	}
	raw := strings.TrimSpace(err.Error())
	lower := strings.ToLower(raw)
	if strings.Contains(lower, "client.timeout exceeded while awaiting headers") ||
		strings.Contains(lower, "timeout awaiting response headers") {
		return "模型服务响应超时，请稍后重试。"
	}
	if errors.Is(err, context.DeadlineExceeded) || strings.Contains(lower, "context deadline exceeded") {
		return "请求处理超时，请稍后重试。"
	}
	if strings.Contains(lower, "output is empty") {
		return "模型服务暂时没有返回有效内容，请稍后重试。"
	}
	for _, status := range []string{"502", "503", "504"} {
		if strings.Contains(lower, status) {
			return "上游服务暂时不可用（" + status + "），请稍后重试。"
		}
	}
	for _, marker := range []string{"429", "rate limit", "rate_limit", "too many requests", "限流"} {
		if strings.Contains(lower, marker) {
			return "模型服务当前请求较多，请稍后重试。"
		}
	}
	for _, marker := range []string{
		"401", "403", "unauthorized", "forbidden", "api key", "apikey",
		"authentication", "permission_error", "quota", "insufficient_quota",
		"billing", "credit", "未授权", "无权限", "额度", "失效",
	} {
		if strings.Contains(lower, marker) {
			return "模型服务配置或额度异常，请联系管理员。"
		}
	}
	return "请求处理失败，请稍后重试。"
}
