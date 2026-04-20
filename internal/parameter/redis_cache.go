package parameter

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
)

// RedisCache implementasi cache untuk parameters
type RedisCache struct {
	client *redis.Client
}

// NewRedisCache membuat redis cache instance
func NewRedisCache(client *redis.Client) *RedisCache {
	return &RedisCache{
		client: client,
	}
}

// Get mengambil parameters dari cache
func (rc *RedisCache) Get(ctx context.Context, key string) (map[string]string, error) {
	data, err := rc.client.Get(ctx, key).Result()
	if err == redis.Nil {
		return nil, nil // Cache miss
	}
	if err != nil {
		return nil, fmt.Errorf("redis get: %w", err)
	}

	var params map[string]string
	if err := json.Unmarshal([]byte(data), &params); err != nil {
		return nil, fmt.Errorf("unmarshal cached params: %w", err)
	}

	return params, nil
}

// Set menyimpan parameters ke cache
func (rc *RedisCache) Set(
	ctx context.Context,
	key string,
	value map[string]string,
	ttl time.Duration,
) error {
	data, err := json.Marshal(value)
	if err != nil {
		return fmt.Errorf("marshal params: %w", err)
	}

	if err := rc.client.Set(ctx, key, data, ttl).Err(); err != nil {
		return fmt.Errorf("redis set: %w", err)
	}

	return nil
}

// Delete menghapus parameters dari cache
func (rc *RedisCache) Delete(ctx context.Context, key string) error {
	if err := rc.client.Del(ctx, key).Err(); err != nil {
		return fmt.Errorf("redis del: %w", err)
	}

	return nil
}

// Flush menghapus semua parameter cache
func (rc *RedisCache) Flush(ctx context.Context) error {
	// Delete all cache keys dengan prefix "params:"
	iter := rc.client.Scan(ctx, 0, "params:*", 1000).Iterator()
	for iter.Next(ctx) {
		if err := rc.client.Del(ctx, iter.Val()).Err(); err != nil {
			return fmt.Errorf("delete cache: %w", err)
		}
	}

	return iter.Err()
}
