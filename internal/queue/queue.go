package queue

import (
	"fmt"

	"tradingengine/pkg/types"
)

type RequestType string

const (
	RequestTypePlace  RequestType = "PLACE"
	RequestTypeCancel RequestType = "CANCEL"
)

type OrderRequest struct {
	Type    RequestType
	Order   *types.Order
	OrderId string
}

type OrderQueue struct {
	ch     chan OrderRequest
	buffer int
}

func NewOrderQueue(buffer int) *OrderQueue {
	return &OrderQueue{
		ch:     make(chan OrderRequest, buffer),
		buffer: buffer,
	}
}

func (q *OrderQueue) Enqueue(order *types.Order) error {
	req := OrderRequest{
		Type:    RequestTypePlace,
		Order:   order,
		OrderId: order.Id,
	}
	select {
	case q.ch <- req:
		return nil
	default:
		return fmt.Errorf("order queue full, order %s rejected", order.Id)
	}
}

func (q *OrderQueue) EnqueueCancel(orderId string,order *types.Order)error{
	req := OrderRequest{
        Type:    RequestTypeCancel,
        OrderId: orderId,
		Order: order,
    }

    select {
    case q.ch <- req:
        return nil
    default:
        return fmt.Errorf("order queue full, cancel for %s rejected", orderId)
    }
}

func (q *OrderQueue) Consume() <-chan OrderRequest{
	return q.ch
}

func (q *OrderQueue) Len() int {
    return len(q.ch)
}

func (q *OrderQueue) Close() {
    close(q.ch)
}