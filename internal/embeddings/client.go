package embeddings

import (
	"codebase/internal/config"
	"context"
	"fmt"
	"os"

	"github.com/sashabaranov/go-openai"
)

type Client struct {
	client *openai.Client
	model  openai.EmbeddingModel
}

func NewClient() *Client {
	apiKey := config.Get("OPENAI_API_KEY", "openai_key")
	if apiKey == "" {
		fmt.Fprintf(os.Stderr, "⚠ Warning: OPENAI_API_KEY is not set\n")
	}

	cfg := openai.DefaultConfig(apiKey)
	if baseURL := config.Get("OPENAI_BASE_URL", "openai_base_url"); baseURL != "" {
		cfg.BaseURL = baseURL
		fmt.Fprintf(os.Stderr, "→ Using custom API endpoint: %s\n", baseURL)
	}

	modelName := config.Get("OPENAI_EMBEDDING_MODEL", "openai_embedding_model")
	model := openai.SmallEmbedding3
	if modelName != "" {
		model = openai.EmbeddingModel(modelName)
		fmt.Fprintf(os.Stderr, "→ Using embedding model: %s\n", modelName)
	}

	return &Client{
		client: openai.NewClientWithConfig(cfg),
		model:  model,
	}
}

func (c *Client) Embed(text string) ([]float32, error) {
	resp, err := c.client.CreateEmbeddings(context.Background(), openai.EmbeddingRequest{
		Model: c.model,
		Input: []string{text},
	})
	if err != nil {
		return nil, err
	}
	if len(resp.Data) == 0 {
		return nil, fmt.Errorf("no embeddings returned")
	}
	return resp.Data[0].Embedding, nil
}

func (c *Client) EmbedBatch(texts []string) ([][]float32, error) {
	if len(texts) == 0 {
		return nil, nil
	}
	resp, err := c.client.CreateEmbeddings(context.Background(), openai.EmbeddingRequest{
		Model: c.model,
		Input: texts,
	})
	if err != nil {
		return nil, err
	}
	results := make([][]float32, len(resp.Data))
	for _, data := range resp.Data {
		results[data.Index] = data.Embedding
	}
	return results, nil
}
