// redis/client.go

package redis

import (
	"context"
	"fmt"
	"log"

	"github.com/go-redis/redis/v8"
)

// Client represents the Redis client.
type Client struct {
	client *redis.Client
}

// NewClient initializes and returns a new Redis client.
func (c *Client) NewClient(redisDSN string) (*redis.Client, error) {
	opt, err := redis.ParseURL(redisDSN)
	if err != nil {
		fmt.Println("error parsing redis dsn: ", err, redisDSN)
		return nil, fmt.Errorf("error parsing redis dsn: %w", err)
	}

	log.Println("Connected to Redis:", opt.Addr)

	c.client = redis.NewClient(opt)

	// ping using Ping
	_, err = c.client.Ping(context.Background()).Result()
	if err != nil {
		return nil, fmt.Errorf("error pinging redis: %w", err)
	}

	return c.client, nil
}

// Close closes the Redis client connection.
func (c *Client) Close() error {
	return c.client.Close()
}
