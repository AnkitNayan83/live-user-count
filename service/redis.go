package service

import (
	"context"
	"encoding/json"
	"log"
	"strconv"

	"github.com/redis/go-redis/v9"
)

var rdb *redis.Client
var ctx = context.Background()

const presenceChannel = "presence_updates"

func InitRedis(addr string, password string) {
	rdb = redis.NewClient(&redis.Options{
		Addr:     addr,
		Password: password,
		DB:       0,
	})
	// simple ping
	_, err := rdb.Ping(ctx).Result()
	if err != nil {
		log.Fatalf("redis connect failed: %v", err)
	}
}

// Keys: page:{pageID}:count
func pageKey(page string) string { return "page:" + page + ":count" }

func IncrCount(page string) (int64, error) {
	return rdb.Incr(ctx, pageKey(page)).Result()
}

func DecrCount(page string) (int64, error) {
	// decr but don't go negative
	val, err := rdb.Decr(ctx, pageKey(page)).Result()
	if err != nil {
		return 0, err
	}
	if val < 0 {
		rdb.Set(ctx, pageKey(page), 0, 0)
		return 0, nil
	}
	return val, nil
}

func GetCount(page string) (int64, error) {
	val, err := rdb.Get(ctx, pageKey(page)).Result()
	if err == redis.Nil {
		return 0, nil
	} else if err != nil {
		return 0, err
	}
	return strconv.ParseInt(val, 10, 64)
}

func publishPresence(page string, count int64) {
	msg := map[string]any{"page": page, "count": count}
	b, _ := json.Marshal(msg)
	_ = rdb.Publish(ctx, presenceChannel, b).Err()
}

// StartRedisSub subscribes to presence updates and forwards them to the hub.
func StartRedisSub(h *Hub) {
	pubsub := rdb.Subscribe(ctx, presenceChannel)
	ch := pubsub.Channel()
	for m := range ch {
		// m.Payload is JSON; forward to hub.broadcast
		h.broadcast <- []byte(m.Payload)
	}
}
