package embeddings

import (
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
	apiKey := os.Getenv("OPENAI_API_KEY")
	return &Client{
		client: openai.NewClient(apiKey),
		model:  openai.SmallEmbedding3,
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
