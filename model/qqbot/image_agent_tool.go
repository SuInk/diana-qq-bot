package qqbot

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"mime"
	"net/url"
	"os"
	"strings"
	"time"

	"diana-qq-bot/model/agent"
	"diana-qq-bot/model/llm"
)

const (
	dianaImageToolName       = "diana.image"
	dianaImageMediaTTL       = 10 * time.Minute
	dianaImageMaxDecodedSize = 32 << 20
	dianaImageTimeoutGrace   = 30 * time.Second
)

type dianaImageTool struct {
	runtime      *Runtime
	event        MessageEvent
	relationship RelationshipPolicy
}

type dianaImageToolResult struct {
	OK      bool   `json:"ok"`
	Queued  bool   `json:"queued"`
	TaskID  string `json:"task_id"`
	Action  string `json:"action"`
	Caption string `json:"caption,omitempty"`
	Reused  bool   `json:"reused,omitempty"`
}

type dianaImageToolRequest struct {
	Operation string
	Prompt    string
	Caption   string
}

type dianaImageTaskOutput struct {
	Caption   string
	ImageURLs []string
}

func newDianaImageTool(runtime *Runtime, event MessageEvent, relationship RelationshipPolicy) agent.Tool {
	return &dianaImageTool{runtime: runtime, event: event, relationship: relationship}
}

func (t *dianaImageTool) Name() string {
	return dianaImageToolName
}

func (t *dianaImageTool) Description() string {
	operations := make([]string, 0, 2)
	if t.relationship.AllowImageGeneration {
		operations = append(operations, `"generate"（根据完整文字 prompt 生成新图片）`)
	}
	if t.relationship.AllowImageEditing {
		operations = append(operations, `"edit"（编辑当前、引用或近期图片/成员头像）`)
	}
	if len(operations) == 0 {
		operations = append(operations, "无")
	}
	return `异步生成或编辑图片。工具会立即返回已受理的任务编号，图片在后台完成后自动发送；调用后必须继续输出 final 文字回复，不要等待图片，也不要再次调用本工具。当前允许操作：` + strings.Join(operations, "、") + `。如果用户要求先搜索、核验网页或读取外部资料再出图，必须先完成搜索/浏览器调用；prompt 必须包含已确认的具体事实、外观和约束，不能虚构未查到的内容。input: {"operation":"generate 或 edit","prompt":"交给图片模型的完整、自包含最终提示词","caption":"图片完成后随图发送的短文字，可选"}`
}

func (t *dianaImageTool) Run(ctx context.Context, input map[string]any) (string, error) {
	if t == nil || t.runtime == nil {
		return "", fmt.Errorf("图片工具未配置")
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	request, err := t.prepareRequest(input)
	if err != nil {
		return "", err
	}
	result, err := t.enqueue(request)
	if err != nil {
		return "", err
	}
	body, err := json.Marshal(result)
	if err != nil {
		return "", err
	}
	return string(body), nil
}

func (t *dianaImageTool) prepareRequest(input map[string]any) (dianaImageToolRequest, error) {
	operation := strings.ToLower(strings.TrimSpace(configToolString(input, "operation")))
	prompt := strings.TrimSpace(configToolString(input, "prompt"))
	if prompt == "" {
		return dianaImageToolRequest{}, fmt.Errorf("prompt 不能为空")
	}
	if len([]rune(prompt)) > 12000 {
		return dianaImageToolRequest{}, fmt.Errorf("prompt 过长，请压缩到 12000 字以内")
	}
	if operation == "" {
		switch {
		case t.relationship.AllowImageGeneration && !t.relationship.AllowImageEditing:
			operation = "generate"
		case !t.relationship.AllowImageGeneration && t.relationship.AllowImageEditing:
			operation = "edit"
		default:
			return dianaImageToolRequest{}, fmt.Errorf("operation 必须是 generate 或 edit")
		}
	}
	switch operation {
	case "generate":
		if !t.relationship.AllowImageGeneration {
			return dianaImageToolRequest{}, fmt.Errorf("%s", relationshipPermissionDenied(t.relationship, "图片生成", relationshipImageTierName))
		}
	case "edit":
		if !t.relationship.AllowImageEditing {
			return dianaImageToolRequest{}, fmt.Errorf("%s", relationshipPermissionDenied(t.relationship, "图片编辑", relationshipImageTierName))
		}
	default:
		return dianaImageToolRequest{}, fmt.Errorf("operation 必须是 generate 或 edit")
	}
	if t.runtime.llmStore == nil {
		return dianaImageToolRequest{}, fmt.Errorf("qqbot: llm profile store is not configured")
	}
	caption := strings.TrimSpace(configToolString(input, "caption"))
	if len([]rune(caption)) > 200 {
		caption = string([]rune(caption)[:200])
	}
	if caption == "" {
		if operation == "edit" {
			caption = "图片编辑完成。"
		} else {
			caption = "图片生成完成。"
		}
	}
	return dianaImageToolRequest{Operation: operation, Prompt: prompt, Caption: caption}, nil
}

func (t *dianaImageTool) enqueue(request dianaImageToolRequest) (dianaImageToolResult, error) {
	name := "图片生成"
	if request.Operation == "edit" {
		name = "图片编辑"
	}
	task := PluginTask{
		Kind:    "image",
		Name:    name,
		Key:     dianaImageTaskKey(t.event, request),
		Timeout: t.taskTimeout(),
		Run: func(ctx context.Context, _ PluginTaskServices) (PluginTaskResult, error) {
			output, err := t.execute(ctx, request)
			if err != nil {
				return PluginTaskResult{}, err
			}
			message := OutgoingMessage{Text: output.Caption, ImageURLs: output.ImageURLs}
			if t.event.Kind == EventKindGroup {
				message.ReplyMessageID = t.event.MessageID
			}
			return PluginTaskResult{Messages: []OutgoingMessage{message}}, nil
		},
	}
	reservation := t.runtime.reservePluginTasks(t.event, []PluginTask{task})
	if !reservation.handled {
		return dianaImageToolResult{}, fmt.Errorf("图片任务无法启动")
	}
	result := dianaImageToolResult{OK: true, Queued: true, Action: request.Operation, Caption: request.Caption}
	if len(reservation.reserved) > 0 {
		result.TaskID = reservation.reserved[0].id
		t.runtime.startPluginTaskReservation(reservation)
		return result, nil
	}
	if len(reservation.duplicates) > 0 {
		result.TaskID = reservation.duplicates[0].ID
		result.Reused = true
		return result, nil
	}
	return dianaImageToolResult{}, fmt.Errorf("图片任务无法启动")
}

func (t *dianaImageTool) taskTimeout() time.Duration {
	cfg := t.runtime.llmStore.Current().WithDefaults()
	timeout := cfg.ImageTimeout + dianaImageTimeoutGrace
	if timeout <= dianaImageTimeoutGrace {
		return defaultSubagentTaskTimeout
	}
	return timeout
}

func (t *dianaImageTool) execute(ctx context.Context, request dianaImageToolRequest) (dianaImageTaskOutput, error) {
	cfg := t.runtime.llmStore.Current().WithDefaults()
	operation := request.Operation
	prompt := request.Prompt
	submittedPrompt := t.runtime.enrichImagePromptWithQQContext(ctx, t.event, prompt)
	var (
		images      []string
		sourceCount int
		action      string
		message     string
	)
	switch operation {
	case "generate":
		resp, err := llm.GenerateImage(ctx, cfg, llm.ImageGenerateRequest{
			Prompt: submittedPrompt,
			Model:  cfg.ImageModelWithDefault(),
			Size:   "1024x1024",
			N:      1,
		})
		if err != nil {
			return dianaImageTaskOutput{}, err
		}
		images = resp.Images
		action = "qqbot.image.generate"
		message = "Agent 图片生成已完成"
	case "edit":
		sources := t.runtime.imageEditSourceImages(ctx, t.event, prompt)
		if len(sources) == 0 {
			return dianaImageTaskOutput{}, fmt.Errorf("没有找到可编辑的图片；请让用户发送图片或引用图片消息")
		}
		resp, err := llm.EditImage(ctx, cfg, llm.ImageEditRequest{
			Prompt: submittedPrompt,
			Images: sources,
			Model:  cfg.ImageModelWithDefault(),
			Size:   "1024x1024",
			N:      1,
		})
		if err != nil {
			return dianaImageTaskOutput{}, err
		}
		images = resp.Images
		sourceCount = len(sources)
		action = "qqbot.image.edit"
		message = "Agent 图片编辑已完成"
	}
	if len(images) == 0 {
		return dianaImageTaskOutput{}, fmt.Errorf("图片接口没有返回图片")
	}

	sharedImages, localPaths, err := t.runtime.shareAgentImages(images)
	if err != nil {
		return dianaImageTaskOutput{}, err
	}
	if len(localPaths) > 0 {
		cleanupLocalMediaFilesLater(localPaths, dianaImageMediaTTL)
	}
	t.runtime.recordImageOperation(ctx, t.event, action, message, prompt, submittedPrompt, cfg.ImageModelWithDefault(), len(images), sourceCount)
	return dianaImageTaskOutput{Caption: request.Caption, ImageURLs: sharedImages}, nil
}

func dianaImageTaskKey(event MessageEvent, request dianaImageToolRequest) string {
	payload := strings.Join([]string{sessionKey(event), event.MessageID, request.Operation, request.Prompt}, "\x00")
	digest := sha256.Sum256([]byte(payload))
	return fmt.Sprintf("image:%x", digest[:12])
}

func asyncImageReplyInstruction(result dianaImageToolResult) string {
	status := "已在后台启动"
	if result.Reused {
		status = "已在后台处理"
	}
	return fmt.Sprintf("【本轮图片任务】%s（任务编号：%s）。立即继续回复用户的文字部分，不要等待图片，不要再调用 diana.image，也不要声称无法生图；图片完成后会由运行时自动补发。", status, result.TaskID)
}

func (r *Runtime) enqueueImageReplyTask(ctx context.Context, event MessageEvent, relationship RelationshipPolicy, operation string, prompt string, caption string) (dianaImageToolResult, error) {
	tool := &dianaImageTool{runtime: r, event: event, relationship: relationship}
	if err := ctx.Err(); err != nil {
		return dianaImageToolResult{}, err
	}
	request, err := tool.prepareRequest(map[string]any{
		"operation": operation,
		"prompt":    prompt,
		"caption":   caption,
	})
	if err != nil {
		return dianaImageToolResult{}, err
	}
	return tool.enqueue(request)
}

func (r *Runtime) shareAgentImages(images []string) ([]string, []string, error) {
	sharedImages := make([]string, 0, len(images))
	localPaths := make([]string, 0, len(images))
	for _, image := range images {
		shared, localPath, err := r.shareAgentImage(image)
		if err != nil {
			for _, path := range localPaths {
				_ = os.Remove(path)
			}
			return nil, nil, err
		}
		sharedImages = append(sharedImages, shared)
		if localPath != "" {
			localPaths = append(localPaths, localPath)
		}
	}
	return sharedImages, localPaths, nil
}

func (r *Runtime) shareAgentImage(image string) (string, string, error) {
	image = strings.TrimSpace(image)
	if parsed, err := url.Parse(image); err == nil && parsed.Host != "" && (parsed.Scheme == "http" || parsed.Scheme == "https") {
		return image, "", nil
	}
	mediaType, encoded, ok := strings.Cut(image, ",")
	mediaType = strings.TrimPrefix(mediaType, "data:")
	if !ok || !strings.HasPrefix(strings.ToLower(mediaType), "image/") || !strings.HasSuffix(strings.ToLower(mediaType), ";base64") {
		return "", "", fmt.Errorf("图片接口返回了不支持的图片地址")
	}
	mediaType = strings.TrimSuffix(mediaType, ";base64")
	if base64.StdEncoding.DecodedLen(len(encoded)) > dianaImageMaxDecodedSize {
		return "", "", fmt.Errorf("图片接口返回的图片超过 32 MiB")
	}
	data, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil || len(data) == 0 {
		return "", "", fmt.Errorf("图片接口返回的 base64 图片无效")
	}
	extension := ".png"
	if extensions, err := mime.ExtensionsByType(mediaType); err == nil && len(extensions) > 0 {
		extension = extensions[0]
	}
	file, err := os.CreateTemp("", "diana-agent-image-*"+extension)
	if err != nil {
		return "", "", err
	}
	path := file.Name()
	if _, err := file.Write(data); err != nil {
		_ = file.Close()
		_ = os.Remove(path)
		return "", "", err
	}
	if err := file.Close(); err != nil {
		_ = os.Remove(path)
		return "", "", err
	}
	r.mu.RLock()
	sharer := r.localMedia
	r.mu.RUnlock()
	if sharer == nil {
		_ = os.Remove(path)
		return "", "", fmt.Errorf("本地媒体共享未配置，无法把生成图片交给 NapCat")
	}
	shared, ok := sharer.Share(path, dianaImageMediaTTL)
	if !ok {
		_ = os.Remove(path)
		return "", "", fmt.Errorf("生成图片无法通过本地媒体代理共享")
	}
	return shared, path, nil
}
