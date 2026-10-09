package summarize

import (
	"context"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/bedrock"
	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/bedrockruntime"
	"github.com/aws/aws-sdk-go-v2/service/bedrockruntime/types"

	"github.com/aldersfors/recap/internal/config"
)

// New builds the provider named in the config. Anthropic reads its
// credentials from ANTHROPIC_API_KEY or an `ant auth login` profile; Bedrock
// and Converse use the AWS credential chain, or the named profile when one is set.
func New(ctx context.Context, cfg config.AI) (Provider, error) {
	switch cfg.Provider {
	case config.ProviderAnthropic:
		client := anthropic.NewClient()
		return &messagesProvider{
			svc:          client.Messages,
			model:        cfg.Anthropic.Model,
			inferenceGeo: cfg.Anthropic.InferenceGeo,
			log:          os.Stderr,
		}, nil
	case config.ProviderBedrock:
		client, err := bedrock.NewMantleClient(ctx, bedrock.MantleClientConfig{
			AWSRegion:  cfg.Bedrock.Region,
			AWSProfile: cfg.Bedrock.Profile,
		})
		if err != nil {
			return nil, fmt.Errorf("bedrock client: %w", err)
		}
		return &messagesProvider{svc: client.Messages, model: cfg.Bedrock.Model}, nil
	case config.ProviderConverse:
		opts := []func(*awsconfig.LoadOptions) error{awsconfig.WithRegion(cfg.Converse.Region)}
		if cfg.Converse.Profile != "" {
			opts = append(opts, awsconfig.WithSharedConfigProfile(cfg.Converse.Profile))
		}
		awsCfg, err := awsconfig.LoadDefaultConfig(ctx, opts...)
		if err != nil {
			return nil, fmt.Errorf("converse client: %w", err)
		}
		return &converseProvider{api: bedrockruntime.NewFromConfig(awsCfg), model: cfg.Converse.Model}, nil
	}
	return nil, fmt.Errorf("unknown provider %q", cfg.Provider)
}

type messagesProvider struct {
	svc   anthropic.MessageService
	model string
	// inferenceGeo is sent as inference_geo when set. Bedrock does not
	// support the parameter, so its provider leaves this empty.
	inferenceGeo string
	// log receives where inference ran, when the API reports it. Nil discards.
	log io.Writer
}

func (p *messagesProvider) Complete(ctx context.Context, system, user string) (string, error) {
	params := anthropic.MessageNewParams{
		Model:     anthropic.Model(p.model),
		MaxTokens: 16000,
		System:    []anthropic.TextBlockParam{{Text: system}},
		Messages: []anthropic.MessageParam{
			anthropic.NewUserMessage(anthropic.NewTextBlock(user)),
		},
	}
	if p.inferenceGeo != "" {
		params.InferenceGeo = anthropic.String(p.inferenceGeo)
	}
	resp, err := p.svc.New(ctx, params)
	if err != nil {
		return "", fmt.Errorf("%s: %w", p.model, err)
	}
	if p.log != nil && resp.Usage.InferenceGeo != "" {
		_, _ = fmt.Fprintf(p.log, "Inference ran in %s.\n", resp.Usage.InferenceGeo)
	}
	switch resp.StopReason {
	case anthropic.StopReasonRefusal:
		return "", fmt.Errorf("%s declined the request (%s): %s", p.model, resp.StopDetails.Category, resp.StopDetails.Explanation)
	case anthropic.StopReasonMaxTokens:
		return "", fmt.Errorf("%s hit max_tokens before finishing the draft", p.model)
	}
	var b strings.Builder
	for _, block := range resp.Content {
		if text, ok := block.AsAny().(anthropic.TextBlock); ok {
			b.WriteString(text.Text)
		}
	}
	return b.String(), nil
}

// converseAPI is the part of the Bedrock Runtime client the converse provider
// uses, so tests can stand in for it.
type converseAPI interface {
	Converse(ctx context.Context, in *bedrockruntime.ConverseInput, optFns ...func(*bedrockruntime.Options)) (*bedrockruntime.ConverseOutput, error)
}

// converseProvider reaches any Bedrock text model through the Converse API,
// including models that are not Anthropic's.
type converseProvider struct {
	api   converseAPI
	model string
}

func (p *converseProvider) Complete(ctx context.Context, system, user string) (string, error) {
	resp, err := p.api.Converse(ctx, &bedrockruntime.ConverseInput{
		ModelId: aws.String(p.model),
		System:  []types.SystemContentBlock{&types.SystemContentBlockMemberText{Value: system}},
		Messages: []types.Message{{
			Role:    types.ConversationRoleUser,
			Content: []types.ContentBlock{&types.ContentBlockMemberText{Value: user}},
		}},
		InferenceConfig: &types.InferenceConfiguration{MaxTokens: aws.Int32(16000)},
	})
	if err != nil {
		return "", fmt.Errorf("%s: %w", p.model, err)
	}
	switch resp.StopReason {
	case types.StopReasonMaxTokens:
		return "", fmt.Errorf("%s hit max_tokens before finishing the draft", p.model)
	case types.StopReasonContentFiltered, types.StopReasonGuardrailIntervened:
		return "", fmt.Errorf("%s declined the request (%s)", p.model, resp.StopReason)
	}
	msg, ok := resp.Output.(*types.ConverseOutputMemberMessage)
	if !ok {
		return "", fmt.Errorf("%s returned no message", p.model)
	}
	var b strings.Builder
	for _, block := range msg.Value.Content {
		if text, ok := block.(*types.ContentBlockMemberText); ok {
			b.WriteString(text.Value)
		}
	}
	return b.String(), nil
}
