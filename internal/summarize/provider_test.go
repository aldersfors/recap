package summarize

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/bedrockruntime"
	"github.com/aws/aws-sdk-go-v2/service/bedrockruntime/types"
)

func fakeMessages(t *testing.T, stopReason, extra string) (*messagesProvider, *map[string]any) {
	t.Helper()
	var got map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/messages" {
			t.Errorf("path = %s", r.URL.Path)
		}
		if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
			t.Error(err)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprintf(w, `{"id":"msg_1","type":"message","role":"assistant","model":"claude-opus-5",
			"content":[{"type":"text","text":"Draft body"}],"stop_reason":%q%s,
			"usage":{"input_tokens":10,"output_tokens":5,"inference_geo":"global"}}`, stopReason, extra)
	}))
	t.Cleanup(srv.Close)
	client := anthropic.NewClient(option.WithBaseURL(srv.URL), option.WithAPIKey("test"), option.WithMaxRetries(0))
	return &messagesProvider{svc: client.Messages, model: "claude-opus-5"}, &got
}

func TestMessagesProviderRequestAndText(t *testing.T) {
	p, got := fakeMessages(t, "end_turn", "")
	text, err := p.Complete(context.Background(), "SYSTEM", "USER")
	if err != nil {
		t.Fatal(err)
	}
	if text != "Draft body" {
		t.Errorf("text = %q", text)
	}
	req := *got
	if req["model"] != "claude-opus-5" {
		t.Errorf("model = %v", req["model"])
	}
	if b, _ := json.Marshal(req["system"]); !strings.Contains(string(b), "SYSTEM") {
		t.Errorf("system = %s", b)
	}
	if b, _ := json.Marshal(req["messages"]); !strings.Contains(string(b), "USER") {
		t.Errorf("messages = %s", b)
	}
}

func TestMessagesProviderInferenceGeo(t *testing.T) {
	p, got := fakeMessages(t, "end_turn", "")
	if _, err := p.Complete(context.Background(), "s", "u"); err != nil {
		t.Fatal(err)
	}
	if v, ok := (*got)["inference_geo"]; ok {
		t.Errorf("inference_geo sent without being configured: %v", v)
	}

	p, got = fakeMessages(t, "end_turn", "")
	var log strings.Builder
	p.inferenceGeo, p.log = "global", &log
	if _, err := p.Complete(context.Background(), "s", "u"); err != nil {
		t.Fatal(err)
	}
	if (*got)["inference_geo"] != "global" {
		t.Errorf("inference_geo = %v", (*got)["inference_geo"])
	}
	if !strings.Contains(log.String(), "Inference ran in global.") {
		t.Errorf("log = %q", log.String())
	}
}

func TestMessagesProviderStopReasons(t *testing.T) {
	p, _ := fakeMessages(t, "refusal", `,"stop_details":{"type":"refusal","category":"cyber","explanation":"no"}`)
	if _, err := p.Complete(context.Background(), "s", "u"); err == nil || !strings.Contains(err.Error(), "declined") {
		t.Errorf("refusal err = %v", err)
	}
	p, _ = fakeMessages(t, "max_tokens", "")
	if _, err := p.Complete(context.Background(), "s", "u"); err == nil || !strings.Contains(err.Error(), "max_tokens") {
		t.Errorf("max_tokens err = %v", err)
	}
}

type fakeConverse struct {
	got  *bedrockruntime.ConverseInput
	resp *bedrockruntime.ConverseOutput
}

func (f *fakeConverse) Converse(_ context.Context, in *bedrockruntime.ConverseInput, _ ...func(*bedrockruntime.Options)) (*bedrockruntime.ConverseOutput, error) {
	f.got = in
	return f.resp, nil
}

func converseReply(stop types.StopReason, blocks ...types.ContentBlock) *bedrockruntime.ConverseOutput {
	return &bedrockruntime.ConverseOutput{
		StopReason: stop,
		Output: &types.ConverseOutputMemberMessage{Value: types.Message{
			Role:    types.ConversationRoleAssistant,
			Content: blocks,
		}},
	}
}

func TestConverseProviderRequestAndText(t *testing.T) {
	api := &fakeConverse{resp: converseReply(types.StopReasonEndTurn,
		&types.ContentBlockMemberReasoningContent{},
		&types.ContentBlockMemberText{Value: "Draft "},
		&types.ContentBlockMemberText{Value: "body"},
	)}
	p := &converseProvider{api: api, model: "zai.glm-5"}
	text, err := p.Complete(context.Background(), "SYSTEM", "USER")
	if err != nil {
		t.Fatal(err)
	}
	if text != "Draft body" {
		t.Errorf("text = %q", text)
	}
	if got := aws.ToString(api.got.ModelId); got != "zai.glm-5" {
		t.Errorf("model = %q", got)
	}
	if sys, ok := api.got.System[0].(*types.SystemContentBlockMemberText); !ok || sys.Value != "SYSTEM" {
		t.Errorf("system = %#v", api.got.System)
	}
	msg := api.got.Messages[0]
	if user, ok := msg.Content[0].(*types.ContentBlockMemberText); msg.Role != types.ConversationRoleUser || !ok || user.Value != "USER" {
		t.Errorf("messages = %#v", api.got.Messages)
	}
}

func TestConverseProviderStopReasons(t *testing.T) {
	for stop, want := range map[types.StopReason]string{
		types.StopReasonMaxTokens:           "max_tokens",
		types.StopReasonContentFiltered:     "declined",
		types.StopReasonGuardrailIntervened: "declined",
	} {
		p := &converseProvider{api: &fakeConverse{resp: converseReply(stop)}, model: "m"}
		if _, err := p.Complete(context.Background(), "s", "u"); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%s err = %v", stop, err)
		}
	}
}
