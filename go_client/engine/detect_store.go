package engine

import (
	"context"
	jsoniter "github.com/json-iterator/go"
	"github.com/redis/go-redis/v9"
	"go_client/config"
	"time"
)

type DetectStore interface {
	StoreDetectResultImage(ctx context.Context, data StoreDetectResultDto) error
	StoreRecordVideo(ctx context.Context, data StoreRecordVideoDto) error
	PushSessionEndNotify(ctx context.Context, sessionID string) error
}

type StoreV1 struct {
	redisClient            *redis.Client
	detectResultChannelKey string
	recordVideoChannelKey  string
	sessionEndChannelKey   string
}

func NewStoreV1WithClient(cfg *config.Config, redisClient *redis.Client) (*StoreV1, error) {
	return &StoreV1{
		redisClient:            redisClient,
		detectResultChannelKey: cfg.Store.DetectResultChannelKey,
		recordVideoChannelKey:  cfg.Store.RecordVideoChannelKey,
		sessionEndChannelKey:   cfg.Store.SessionEndChannelKey,
	}, nil
}

func NewStoreV1(cfg *config.Config) (*StoreV1, error) {
	client := redis.NewClient(&redis.Options{
		Addr:            cfg.Redis.Host,
		Password:        cfg.Redis.Password,
		MinIdleConns:    cfg.Redis.MinIdleConns,
		MaxIdleConns:    cfg.Redis.MaxIdleConns,
		ConnMaxIdleTime: time.Minute * 30,
	})

	ctx, cancel := context.WithTimeout(context.Background(), time.Second*15)
	defer cancel()
	if result := client.Ping(ctx); result.Err() != nil {
		return nil, result.Err()
	}

	return &StoreV1{
		redisClient:            client,
		detectResultChannelKey: cfg.Store.DetectResultChannelKey,
		recordVideoChannelKey:  cfg.Store.RecordVideoChannelKey,
		sessionEndChannelKey:   cfg.Store.SessionEndChannelKey,
	}, nil
}

// StoreDetectResultDto 存储识别结果Dto
type StoreDetectResultDto struct {
	Labels    []string `json:"labels"`    // 标签
	PathURL   string   `json:"pathURL"`   // 路径地址
	Timestamp int64    `json:"timestamp"` // 识别时间
	ID        string   `json:"id"`        // 识别会话ID
}

// StoreRecordVideoDto 存储视频结果Dto
type StoreRecordVideoDto struct {
	PathURL        string `json:"pathURL"`        // 路径地址
	StartTimestamp int64  `json:"startTimestamp"` // 开始时间戳
	EndTimestamp   int64  `json:"endTimestamp"`   // 结束时间戳
	ID             string `json:"id"`             // 识别会话ID
}

func (s StoreV1) StoreDetectResultImage(ctx context.Context, data StoreDetectResultDto) error {

	_data, _ := jsoniter.MarshalToString(data)

	return s.redisClient.Publish(ctx, s.detectResultChannelKey, _data).Err()
}

func (s StoreV1) StoreRecordVideo(ctx context.Context, data StoreRecordVideoDto) error {
	_data, _ := jsoniter.MarshalToString(data)

	return s.redisClient.Publish(ctx, s.recordVideoChannelKey, _data).Err()
}

func (s StoreV1) PushSessionEndNotify(ctx context.Context, sessionID string) error {
	return s.redisClient.Publish(ctx, s.sessionEndChannelKey, sessionID).Err()
}
