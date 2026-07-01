package types
import(
	"errors"
)

var (
    ErrOrderNotFound      = errors.New("order not found")
    ErrInvalidQuantity    = errors.New("quantity must be greater than zero")
    ErrInvalidPrice       = errors.New("price must be greater than zero for limit orders")
    ErrInvalidSymbol      = errors.New("symbol is invalid or not supported")
    ErrOrderAlreadyFilled = errors.New("order is already filled")
    ErrOrderCancelled     = errors.New("order has been cancelled")
    ErrInsufficientFunds  = errors.New("insufficient funds")
    ErrUnknownSide        = errors.New("unknown order side")
    ErrUnknownOrderType   = errors.New("unknown order type")
)