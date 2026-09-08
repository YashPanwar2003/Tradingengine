package order

import (
	"context"
	"encoding/json"
	"fmt"
	"time"
	"tradingengine/pkg/types"

	"github.com/redis/go-redis/v9"
)

type RedisStore struct {
	orders *redis.Client
}

func (r *RedisStore) Save(order *types.Order) error {
	ctx := context.Background()
	data, err := json.Marshal(order)
	if err != nil {
		return fmt.Errorf("Serializing object:%w", err)
	}
	err1 := r.orders.Set(ctx, order.Id, data, time.Hour).Err()
	if err1 !=nil{
		return fmt.Errorf("Error while storing: %w",err)
	}
    return nil
}
