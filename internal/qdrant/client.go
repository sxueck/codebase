package qdrant

import (
	"context"
	"fmt"
	"os"

	"github.com/qdrant/go-client/qdrant"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

type Client struct {
	client       qdrant.PointsClient
	collections  qdrant.CollectionsClient
	grpcConn     *grpc.ClientConn
}

func NewClient() (*Client, error) {
	url := os.Getenv("QDRANT_URL")
	if url == "" {
		url = "localhost:6334"
	}

	conn, err := grpc.Dial(url, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return nil, err
	}

	return &Client{
		client:      qdrant.NewPointsClient(conn),
		collections: qdrant.NewCollectionsClient(conn),
		grpcConn:    conn,
	}, nil
}

func (c *Client) Close() error {
	return c.grpcConn.Close()
}

func (c *Client) EnsureCollection(name string, vectorSize uint64) error {
	ctx := context.Background()

	_, err := c.collections.Get(ctx, &qdrant.GetCollectionInfoRequest{
		CollectionName: name,
	})

	if err == nil {
		return nil
	}

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

func (c *Client) Upsert(collectionName string, points []*qdrant.PointStruct) error {
	ctx := context.Background()
	_, err := c.client.Upsert(ctx, &qdrant.UpsertPoints{
		CollectionName: collectionName,
		Points:         points,
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
