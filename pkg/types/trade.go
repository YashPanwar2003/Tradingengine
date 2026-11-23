package types

import (
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
)

type Trade struct {
	Id          string          `json:"id"`
	Symbol      string          `json:"symbol"`
	BuyOrderId  string          `json:"buyOrderId"`
	SellOrderId string          `json:"sellOrderId"`
	Price       decimal.Decimal `json:"price"`
	Quantity    decimal.Decimal `json:"quantity"`
	ExecutedAt  time.Time       `json:"executedAt"`
}

func NewTrade(symbol, BuyOrderId, SellOrderId string, price, quantity decimal.Decimal, executedAt time.Time) *Trade {
	return &Trade{
		Id:          uuid.New().String(),
		Symbol:      symbol,
		BuyOrderId:  BuyOrderId,
		SellOrderId: SellOrderId,
		Price:       price,
		Quantity:    quantity,
		ExecutedAt:  executedAt,
	}
}
