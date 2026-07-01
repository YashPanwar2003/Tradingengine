package types

import(
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
)

type Trade struct{
	Id string
	Symbol string
	BuyOrderId string
	SellOrderId string
	Price decimal.Decimal
	Quantity decimal.Decimal
	ExecutedAt time.Time
}

func NewTrade(symbol , BuyOrderId,SellOrderId string , price , quantity decimal.Decimal , executedAt time.Time) *Trade{
    return &Trade{
		Id: uuid.New().String(),
		Symbol: symbol,
		BuyOrderId: BuyOrderId,
		SellOrderId: SellOrderId,
		Price: price,
		Quantity: quantity,
		ExecutedAt: executedAt,
	}
}