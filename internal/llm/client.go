package llm

import (
	"codebase/internal/models"
	"context"
	"encoding/json"
	"os"

	"github.com/sashabaranov/go-openai"
)

type Client struct {
	client *openai.Client
}

func NewClient() *Client {
	apiKey := os.Getenv("OPENAI_API_KEY")
	return &Client{
		client: openai.NewClient(apiKey),
	}
}

func (c *Client) BuildQueryPlan(query string) (*models.QueryPlan, error) {
	systemPrompt := `你是代码查询规划器，只输出JSON格式，不输出多余文字。
根据用户的中文或英文描述，分析意图并生成结构化的查询计划。

Intent类型说明:
- SEARCH: 一般语义检索
- DUPLICATE: 查找重复逻辑
- REFACTOR: 查找可重构点
- BUG_PATTERN: 按模式找潜在bug

输出JSON格式示例:
{
  "intent": "DUPLICATE",
  "sub_queries": ["重复实现", "功能相同但实现不同"],
  "filter": {
    "languages": ["go", "python", "typescript"],
    "node_types": ["function", "method"],
    "min_lines": 5,
    "max_lines": 300
  },
  "threshold": 0.92
}`

	resp, err := c.client.CreateChatCompletion(context.Background(), openai.ChatCompletionRequest{
		Model: openai.GPT4TurboPreview,
		Messages: []openai.ChatCompletionMessage{
			{Role: openai.ChatMessageRoleSystem, Content: systemPrompt},
			{Role: openai.ChatMessageRoleUser, Content: query},
		},
		ResponseFormat: &openai.ChatCompletionResponseFormat{
			Type: openai.ChatCompletionResponseFormatTypeJSONObject,
		},
	})
	if err != nil {
		return nil, err
	}

	var plan models.QueryPlan
	if err := json.Unmarshal([]byte(resp.Choices[0].Message.Content), &plan); err != nil {
		return nil, err
	}

	return &plan, nil
}

func (c *Client) ClassifyDuplicatePair(a, b models.CodeChunkPayload, score float64) (bool, string, error) {
	prompt := `判断以下两段代码是否为逻辑重复。只返回JSON格式。

代码A (` + a.Language + `, ` + a.FilePath + `):
` + a.Content + `

代码B (` + b.Language + `, ` + b.FilePath + `):
` + b.Content + `

相似度分数: ` + string(rune(int(score*100))) + `%

输出JSON格式:
{
  "classification": "DUPLICATE" 或 "NOT_DUPLICATE",
  "reason": "判断理由"
}`

	resp, err := c.client.CreateChatCompletion(context.Background(), openai.ChatCompletionRequest{
		Model: openai.GPT4TurboPreview,
		Messages: []openai.ChatCompletionMessage{
			{Role: openai.ChatMessageRoleUser, Content: prompt},
		},
		ResponseFormat: &openai.ChatCompletionResponseFormat{
			Type: openai.ChatCompletionResponseFormatTypeJSONObject,
		},
	})
	if err != nil {
		return false, "", err
	}

	var result struct {
		Classification string `json:"classification"`
		Reason         string `json:"reason"`
	}
	if err := json.Unmarshal([]byte(resp.Choices[0].Message.Content), &result); err != nil {
		return false, "", err
	}

	return result.Classification == "DUPLICATE", result.Reason, nil
}
