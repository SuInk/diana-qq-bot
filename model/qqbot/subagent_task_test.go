package qqbot

import (
	"context"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestSubagentTaskAcknowledgesThenSendsUnquotedFollowup(t *testing.T) {
	channel := &concurrentRecordingChannel{}
	runtime := NewRuntime(BotConfig{BotQQ: "42"}, channel, NewPluginManager(), nil, nil, nil, nil)
	event := MessageEvent{Kind: EventKindGroup, GroupID: "123456", UserID: "10001", MessageID: "message-1"}
	task := PluginTask{
		Kind:           "test",
		Name:           "测试任务",
		Key:            "test:one",
		StartedMessage: "测试任务已开始",
		Run: func(context.Context, PluginTaskServices) (PluginTaskResult, error) {
			return PluginTaskResult{Reply: "后台任务结果"}, nil
		},
	}

	ack, handled, err := runtime.launchPluginTasks(context.Background(), event, []PluginTask{task})
	if err != nil || !handled || ack == "" {
		t.Fatalf("launchPluginTasks() = %q, %v, %v", ack, handled, err)
	}
	waitForCondition(t, time.Second, func() bool { return channel.count() == 2 })
	messages := channel.messages()
	if messages[0].ReplyMessageID != event.MessageID || messages[0].MentionUserID != event.UserID {
		t.Fatalf("ack message = %#v", messages[0])
	}
	if messages[1].Text != "后台任务结果" || messages[1].ReplyMessageID != "" || messages[1].MentionUserID != "" {
		t.Fatalf("followup message = %#v", messages[1])
	}
}

func TestRuntimeLaunchesPluginTaskWithoutCallingForegroundLLM(t *testing.T) {
	channel := &concurrentRecordingChannel{}
	runtime := NewRuntime(BotConfig{BotQQ: "42"}, channel, NewPluginManager(taskProducingPlugin{}), nil, nil, nil, nil)
	event := MessageEvent{Kind: EventKindPrivate, UserID: "10001", MessageID: "message-1"}
	reply, err := runtime.replyTo(context.Background(), event, "处理这个文件")
	if err != nil {
		t.Fatal(err)
	}
	if reply == "" || !strings.Contains(reply, "任务编号") {
		t.Fatalf("reply = %q", reply)
	}
	waitForCondition(t, time.Second, func() bool { return channel.count() == 2 })
	if got := channel.messages()[1].Text; got != "插件后台结果" {
		t.Fatalf("followup = %q", got)
	}
}

func TestSubagentTaskDeduplicatesActiveWork(t *testing.T) {
	channel := &concurrentRecordingChannel{}
	runtime := NewRuntime(BotConfig{BotQQ: "42"}, channel, NewPluginManager(), nil, nil, nil, nil)
	event := MessageEvent{Kind: EventKindPrivate, UserID: "10001", MessageID: "message-1"}
	release := make(chan struct{})
	var runs atomic.Int64
	task := PluginTask{
		Kind: "test",
		Name: "去重任务",
		Key:  "same-key",
		Run: func(ctx context.Context, _ PluginTaskServices) (PluginTaskResult, error) {
			runs.Add(1)
			select {
			case <-release:
				return PluginTaskResult{Reply: "完成"}, nil
			case <-ctx.Done():
				return PluginTaskResult{}, ctx.Err()
			}
		},
	}

	if _, handled, err := runtime.launchPluginTasks(context.Background(), event, []PluginTask{task}); err != nil || !handled {
		t.Fatalf("first launch: handled=%v err=%v", handled, err)
	}
	waitForCondition(t, time.Second, func() bool { return runs.Load() == 1 })
	if _, handled, err := runtime.launchPluginTasks(context.Background(), event, []PluginTask{task}); err != nil || !handled {
		t.Fatalf("second launch: handled=%v err=%v", handled, err)
	}
	if runs.Load() != 1 {
		t.Fatalf("runs = %d, want 1", runs.Load())
	}
	close(release)
	waitForCondition(t, time.Second, func() bool { return runtime.activeSubagentTaskCount() == 0 })
}

func TestSubagentTaskReservationStartsOnlyAfterExplicitStart(t *testing.T) {
	channel := &concurrentRecordingChannel{}
	runtime := NewRuntime(BotConfig{BotQQ: "42"}, channel, NewPluginManager(), nil, nil, nil, nil)
	release := make(chan struct{})
	task := PluginTask{
		Kind: "test",
		Name: "静默启动任务",
		Key:  "silent-task",
		Run: func(context.Context, PluginTaskServices) (PluginTaskResult, error) {
			<-release
			return PluginTaskResult{Reply: "完成"}, nil
		},
	}

	reservation := runtime.reservePluginTasks(MessageEvent{Kind: EventKindPrivate, UserID: "10001"}, []PluginTask{task})
	if !reservation.handled || !strings.Contains(reservation.ack, "任务编号") {
		t.Fatalf("reservation = %#v", reservation)
	}
	if channel.count() != 0 || runtime.activeSubagentTaskCount() != 1 {
		t.Fatalf("messages = %d active = %d", channel.count(), runtime.activeSubagentTaskCount())
	}
	runtime.startPluginTaskReservation(reservation)
	close(release)
	waitForCondition(t, time.Second, func() bool { return channel.count() == 1 })
	if channel.messages()[0].Text != "完成" {
		t.Fatalf("messages = %#v", channel.messages())
	}
}

type concurrentRecordingChannel struct {
	mu   sync.Mutex
	sent []OutgoingMessage
}

type taskProducingPlugin struct{}

func (taskProducingPlugin) Manifest() PluginManifest {
	return PluginManifest{ID: "test.task-producing", Name: "Task Producer", Version: "0.1.0", BuiltIn: true}
}

func (taskProducingPlugin) Handle(context.Context, PluginRequest) (*PluginResponse, error) {
	return &PluginResponse{Handled: true, Tasks: []PluginTask{{
		Kind: "test",
		Name: "插件任务",
		Key:  "plugin-task",
		Run: func(context.Context, PluginTaskServices) (PluginTaskResult, error) {
			return PluginTaskResult{Reply: "插件后台结果"}, nil
		},
	}}}, nil
}

func (c *concurrentRecordingChannel) Connect(context.Context, EventHandler) error { return nil }

func (c *concurrentRecordingChannel) Send(_ context.Context, msg OutgoingMessage) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.sent = append(c.sent, msg)
	return nil
}

func (c *concurrentRecordingChannel) CallAPI(context.Context, string, map[string]any) (map[string]any, error) {
	return map[string]any{"message_id": 42}, nil
}

func (c *concurrentRecordingChannel) Status() ChannelStatus { return ChannelStatus{Connected: true} }
func (c *concurrentRecordingChannel) Close() error          { return nil }

func (c *concurrentRecordingChannel) count() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.sent)
}

func (c *concurrentRecordingChannel) messages() []OutgoingMessage {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]OutgoingMessage(nil), c.sent...)
}
