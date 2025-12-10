package qdrant

import (
	"codebase/internal/config"
	"context"
	"errors"
	"fmt"
	"net"
	neturl "net/url"
	"strconv"
	"strings"

	"github.com/qdrant/go-client/qdrant"
	"google.golang.org/grpc"
)

type Client struct {
	client      qdrant.PointsClient
	collections qdrant.CollectionsClient
	grpcConn    *grpc.ClientConn
}

func NewClient() (*Client, error) {
	addr := config.Get("QDRANT_URL", "qdrant_url")
	host, port, err := parseQdrantAddress(addr)
	if err != nil {
		return nil, err
	}

	cfg := &qdrant.Config{
		Host: host,
		Port: port,
	}

	if apiKey := getQdrantAPIKey(); apiKey != "" {
		cfg.APIKey = apiKey
	}

	grpcClient, err := qdrant.NewGrpcClient(cfg)
	if err != nil {
		return nil, err
	}

	return &Client{
		client:      grpcClient.Points(),
		collections: grpcClient.Collections(),
		grpcConn:    grpcClient.Conn(),
	}, nil
}

func parseQdrantAddress(raw string) (string, int, error) {
	const (
		defaultHost = "localhost"
		defaultPort = 6334
	)

	if strings.TrimSpace(raw) == "" {
		return defaultHost, defaultPort, nil
	}

	endpoint := strings.TrimSpace(raw)
	if strings.Contains(endpoint, "://") {
		parsed, err := neturl.Parse(endpoint)
		if err != nil {
			return "", 0, err
		}
		if parsed.Host == "" {
			return defaultHost, defaultPort, nil
		}
		endpoint = parsed.Host
	}

	host, portStr, err := net.SplitHostPort(endpoint)
	if err != nil {
		var addrErr *net.AddrError
		if errors.As(err, &addrErr) && strings.Contains(addrErr.Err, "missing port") {
			return endpoint, defaultPort, nil
		}
		return "", 0, err
	}

	port, err := strconv.Atoi(portStr)
	if err != nil {
		return "", 0, err
	}
	if host == "" {
		host = defaultHost
	}

	return host, port, nil
}

func getQdrantAPIKey() string {
	return config.Get(
		"QDRANT_API_KEY",
		"qdrant_api_key",
		"QDRANT_API_TOKEN",
		"qdrant_api_token",
		"QDRANT_AUTH_TOKEN",
		"qdrant_auth_token",
		"QDRANT_AUTH_PASSWORD",
		"qdrant_auth_password",
		"QDRANT_PASSWORD",
		"qdrant_password",
	)
}

func (c *Client) Close() error {
	return c.grpcConn.Close()
}

func (c *Client) EnsureCollection(name string, vectorSize uint64) error {
	ctx := context.Background()

	info, err := c.collections.Get(ctx, &qdrant.GetCollectionInfoRequest{
		CollectionName: name,
	})

	if err == nil {
		// Collection exists, check if vector size matches
		if params := info.GetResult().GetConfig().GetParams(); params != nil {
			existingSize := params.GetVectorsConfig().GetParams().GetSize()
			if existingSize != vectorSize {
				fmt.Printf("⚠ Collection exists with wrong dimension (expected %d, got %d). Deleting and recreating...\n", vectorSize, existingSize)
				// Delete the old collection
				_, err := c.collections.Delete(ctx, &qdrant.DeleteCollection{
					CollectionName: name,
				})
				if err != nil {
					return fmt.Errorf("failed to delete collection: %w", err)
				}
				fmt.Println("✓ Old collection deleted")
			} else {
				return nil
			}
		} else {
			return nil
		}
	}

	// Create new collection with correct size
	_, err = c.collections.Create(ctx, &qdrant.CreateCollection{
		CollectionName: name,
		VectorsConfig: &qdrant.VectorsConfig{
			Config: &qdrant.VectorsConfig_Params{
				Params: &qdrant.VectorParams{
					Size:     vectorSize,
					Distance: qdrant.Distance_Cosine,
				},
			},
		},
	})
	return err
}

// DeleteCollection removes the entire collection and all its points from Qdrant.
func (c *Client) DeleteCollection(name string) error {
	ctx := context.Background()
	_, err := c.collections.Delete(ctx, &qdrant.DeleteCollection{
		CollectionName: name,
	})
	return err
}

func (c *Client) Upsert(collectionName string, points []*qdrant.PointStruct) error {
	ctx := context.Background()

	// Set wait=true to ensure operation completes before returning
	wait := true
	_, err := c.client.Upsert(ctx, &qdrant.UpsertPoints{
		CollectionName: collectionName,
		Points:         points,
		Wait:           &wait,
	})

	return err
}

func (c *Client) Search(collectionName string, vector []float32, limit uint64) ([]*qdrant.ScoredPoint, error) {
	ctx := context.Background()
	resp, err := c.client.Search(ctx, &qdrant.SearchPoints{
		CollectionName: collectionName,
		Vector:         vector,
		Limit:          limit,
		WithPayload:    &qdrant.WithPayloadSelector{SelectorOptions: &qdrant.WithPayloadSelector_Enable{Enable: true}},
	})
	if err != nil {
		return nil, err
	}
	return resp.Result, nil
}

func (c *Client) Scroll(collectionName string, limit uint32, offset *qdrant.PointId) ([]*qdrant.RetrievedPoint, *qdrant.PointId, error) {
	ctx := context.Background()
	resp, err := c.client.Scroll(ctx, &qdrant.ScrollPoints{
		CollectionName: collectionName,
		Limit:          &limit,
		Offset:         offset,
		WithPayload:    &qdrant.WithPayloadSelector{SelectorOptions: &qdrant.WithPayloadSelector_Enable{Enable: true}},
		WithVectors:    &qdrant.WithVectorsSelector{SelectorOptions: &qdrant.WithVectorsSelector_Enable{Enable: true}},
	})
	if err != nil {
		return nil, nil, err
	}
	return resp.Result, resp.NextPageOffset, nil
}

func (c *Client) DeleteByFilter(collectionName string, filter *qdrant.Filter) error {
	ctx := context.Background()
	_, err := c.client.Delete(ctx, &qdrant.DeletePoints{
		CollectionName: collectionName,
		Points: &qdrant.PointsSelector{
			PointsSelectorOneOf: &qdrant.PointsSelector_Filter{
				Filter: filter,
			},
		},
	})
	return err
}

func PayloadToMap(payload map[string]*qdrant.Value) map[string]interface{} {
	result := make(map[string]interface{})
	for k, v := range payload {
		result[k] = valueToInterface(v)
	}
	return result
}

func valueToInterface(v *qdrant.Value) interface{} {
	if v == nil {
		return nil
	}
	switch val := v.Kind.(type) {
	case *qdrant.Value_StringValue:
		return val.StringValue
	case *qdrant.Value_IntegerValue:
		return val.IntegerValue
	case *qdrant.Value_DoubleValue:
		return val.DoubleValue
	case *qdrant.Value_BoolValue:
		return val.BoolValue
	default:
		return fmt.Sprintf("%v", v)
	}
}

func MapToPayload(m map[string]interface{}) map[string]*qdrant.Value {
	result := make(map[string]*qdrant.Value)
	for k, v := range m {
		result[k] = interfaceToValue(v)
	}
	return result
}

func interfaceToValue(i interface{}) *qdrant.Value {
	switch v := i.(type) {
	case string:
		return &qdrant.Value{Kind: &qdrant.Value_StringValue{StringValue: v}}
	case int:
		return &qdrant.Value{Kind: &qdrant.Value_IntegerValue{IntegerValue: int64(v)}}
	case int64:
		return &qdrant.Value{Kind: &qdrant.Value_IntegerValue{IntegerValue: v}}
	case float64:
		return &qdrant.Value{Kind: &qdrant.Value_DoubleValue{DoubleValue: v}}
	case bool:
		return &qdrant.Value{Kind: &qdrant.Value_BoolValue{BoolValue: v}}
	default:
		return &qdrant.Value{Kind: &qdrant.Value_StringValue{StringValue: fmt.Sprintf("%v", v)}}
	}
}
