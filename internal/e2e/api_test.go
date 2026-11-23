package e2e

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/shopspring/decimal"

	"tradingengine/internal/db"
	"tradingengine/internal/gateway"
	"tradingengine/internal/matching"
	"tradingengine/internal/order"
	"tradingengine/internal/queue"
	"tradingengine/internal/server"
	"tradingengine/internal/testutil"
	"tradingengine/pkg/types"
)

const apiKey = "test-key"

// stack is one full "process lifetime": memory store, book, engine, DB worker
// and an HTTP server. Only Postgres survives between two stacks.
type stack struct {
	URL  string
	stop func()
}

func (s *stack) Stop() { s.stop() }

func startStack(t *testing.T, client *db.Client) *stack {
	t.Helper()

	storage := db.NewDBStorage(client)
	mem := order.NewMemoryStore()
	ps := order.NewPersistentStore(mem, storage, 100)
	go ps.Run()
	svc := order.NewService(ps)
	q := queue.NewOrderQueue(100)
	engine := matching.NewEngine(q, svc, 100)

	engineCtx, stopEngine := context.WithCancel(context.Background())
	tradesCtx, stopTrades := context.WithCancel(context.Background())

	// drain trades so the matcher can never block on a full channel
	tradesDone := make(chan struct{})
	go func() {
		defer close(tradesDone)
		for {
			select {
			case <-engine.Trades():
			case <-tradesCtx.Done():
				return
			}
		}
	}()

	// same startup order as main(): restore the book before the engine runs
	active, err := storage.ListActiveOrders(context.Background())
	if err != nil {
		t.Fatalf("list active orders: %v", err)
	}
	for _, o := range active {
		if err := mem.Save(o); err != nil {
			t.Fatalf("restore into memory: %v", err)
		}
	}
	engine.Restore(active)

	engineDone := make(chan struct{})
	go func() {
		defer close(engineDone)
		engine.Run(engineCtx)
	}()

	srv := httptest.NewServer(server.NewRouter(
		gateway.NewGateway(svc, q, gateway.Readers{Books: engine, Trades: storage, Health: client}),
		apiKey))

	var once sync.Once
	s := &stack{URL: srv.URL}
	s.stop = func() {
		once.Do(func() {
			srv.Close()
			stopEngine()
			<-engineDone
			stopTrades()
			<-tradesDone
			ps.Close() // flushes every queued database write
		})
	}
	t.Cleanup(s.Stop)
	return s
}

// ---- helpers ----

func (s *stack) do(t *testing.T, method, path, body, key string) (int, []byte) {
	t.Helper()
	req, err := http.NewRequest(method, s.URL+path, strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	if key != "" {
		req.Header.Set("X-Engine-Key", key)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, raw
}

func (s *stack) place(t *testing.T, clientID, side string, price, qty int64, tif string) types.Order {
	t.Helper()
	body := fmt.Sprintf(
		`{"clientId":%q,"symbol":"BTC-USD","side":%q,"type":"LIMIT","price":"%d","quantity":"%d","timeInForce":%q}`,
		clientID, side, price, qty, tif)
	code, raw := s.do(t, http.MethodPost, "/orders", body, apiKey)
	if code != http.StatusCreated {
		t.Fatalf("place order: status %d, body %s", code, raw)
	}
	var out struct {
		Order types.Order `json:"order"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("decode place response: %v (%s)", err, raw)
	}
	return out.Order
}

func (s *stack) get(t *testing.T, id string) types.Order {
	t.Helper()
	code, raw := s.do(t, http.MethodGet, "/orders/"+id, "", apiKey)
	if code != http.StatusOK {
		t.Fatalf("get order %s: status %d, body %s", id, code, raw)
	}
	var o types.Order
	if err := json.Unmarshal(raw, &o); err != nil {
		t.Fatalf("decode order: %v (%s)", err, raw)
	}
	return o
}

// waitFor polls until the engine (which works asynchronously) has moved the
// order to the expected state.
func (s *stack) waitFor(t *testing.T, id string, status types.OrderStatus, filled int64) {
	t.Helper()
	var last types.Order
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		last = s.get(t, id)
		if last.Status == status && last.Filled.Equal(decimal.NewFromInt(filled)) {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("order %s: want %s/filled=%d, last seen %s/filled=%s",
		id, status, filled, last.Status, last.Filled)
}

func countRows(t *testing.T, c *db.Client, sql string) int {
	t.Helper()
	var n int
	if err := c.QueryRow(context.Background(), sql).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

// ---- tests ----

func TestAPI_PlaceMatchAndQuery(t *testing.T) {
	client := testutil.NewDB(t)
	s := startStack(t, client)

	sell := s.place(t, "c1", "SELL", 100, 5, "GTC")
	buy := s.place(t, "c2", "BUY", 100, 2, "GTC")

	s.waitFor(t, sell.Id, types.OrderStatusPartial, 2)
	s.waitFor(t, buy.Id, types.OrderStatusCompleted, 2)

	code, raw := s.do(t, http.MethodGet, "/clients/c1/orders", "", apiKey)
	if code != http.StatusOK || !strings.Contains(string(raw), sell.Id) {
		t.Fatalf("client orders: status %d, body %s", code, raw)
	}

	s.Stop() // flush the DB worker, then inspect Postgres
	if n := countRows(t, client, "SELECT count(*) FROM trades"); n != 1 {
		t.Fatalf("expected 1 trade, got %d", n)
	}
	testutil.AssertFillsMatchTrades(t, client)
}

func TestAPI_RequiresKey(t *testing.T) {
	client := testutil.NewDB(t)
	s := startStack(t, client)

	if code, _ := s.do(t, http.MethodGet, "/clients/c1/orders", "", ""); code != http.StatusUnauthorized {
		t.Fatalf("no key: expected 401, got %d", code)
	}
	if code, _ := s.do(t, http.MethodGet, "/clients/c1/orders", "", "wrong"); code != http.StatusUnauthorized {
		t.Fatalf("wrong key: expected 401, got %d", code)
	}
	if code, _ := s.do(t, http.MethodGet, "/health", "", ""); code != http.StatusOK {
		t.Fatalf("/health needs no key, got %d", code)
	}
}

func TestAPI_UnknownOrderIs404(t *testing.T) {
	client := testutil.NewDB(t)
	s := startStack(t, client)

	code, _ := s.do(t, http.MethodGet, "/orders/00000000-0000-0000-0000-000000000000", "", apiKey)
	if code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", code)
	}
}

func TestAPI_IOCRemainderIsCancelled(t *testing.T) {
	client := testutil.NewDB(t)
	s := startStack(t, client)

	sell := s.place(t, "c1", "SELL", 100, 3, "GTC")
	ioc := s.place(t, "c2", "BUY", 100, 10, "IOC")

	s.waitFor(t, sell.Id, types.OrderStatusCompleted, 3)
	s.waitFor(t, ioc.Id, types.OrderStatusCancelled, 3)

	s.Stop()
	testutil.AssertFillsMatchTrades(t, client)
}

func TestAPI_CancelRemovesOrderFromTheBook(t *testing.T) {
	client := testutil.NewDB(t)
	s := startStack(t, client)

	sell := s.place(t, "c1", "SELL", 100, 5, "GTC")

	code, raw := s.do(t, http.MethodDelete, "/orders/"+sell.Id+"?client_id=c1", "", apiKey)
	if code != http.StatusOK {
		t.Fatalf("cancel: status %d, body %s", code, raw)
	}
	s.waitFor(t, sell.Id, types.OrderStatusCancelled, 0)

	// nothing is left to trade against, so an IOC buy must be cancelled untouched
	ioc := s.place(t, "c2", "BUY", 100, 5, "IOC")
	s.waitFor(t, ioc.Id, types.OrderStatusCancelled, 0)

	s.Stop()
	if n := countRows(t, client, "SELECT count(*) FROM trades"); n != 0 {
		t.Fatalf("expected no trades, got %d", n)
	}
}

// The headline persistence test, over HTTP: a resting order survives a restart
// and still trades afterwards.
func TestAPI_RestartKeepsRestingOrders(t *testing.T) {
	client := testutil.NewDB(t)

	s1 := startStack(t, client)
	sell := s1.place(t, "c1", "SELL", 100, 5, "GTC")
	s1.Stop()

	s2 := startStack(t, client) // new memory, book and engine: only Postgres survived
	if got := s2.get(t, sell.Id); got.Status != types.OrderStatusNew {
		t.Fatalf("restored order should be NEW, got %s", got.Status)
	}
	buy := s2.place(t, "c2", "BUY", 100, 3, "GTC")
	s2.waitFor(t, buy.Id, types.OrderStatusCompleted, 3)
	s2.waitFor(t, sell.Id, types.OrderStatusPartial, 3)

	s2.Stop()
	if n := countRows(t, client, "SELECT count(*) FROM trades"); n != 1 {
		t.Fatalf("expected 1 trade, got %d", n)
	}
	testutil.AssertFillsMatchTrades(t, client)
}

// The next two need the validation fixes from the "small fixes" block
// (400 for bad input, 403 for a wrong owner). Until they are applied,
// the bad-input cases come back as 500 and the wrong-owner cancel as 500.

func TestAPI_RejectsBadInput(t *testing.T) {
	client := testutil.NewDB(t)
	s := startStack(t, client)

	cases := map[string]string{
		"missing clientId":    `{"symbol":"BTC-USD","side":"BUY","type":"LIMIT","price":"100","quantity":"1","timeInForce":"GTC"}`,
		"unknown side":        `{"clientId":"c1","symbol":"BTC-USD","side":"SIDEWAYS","type":"LIMIT","price":"100","quantity":"1","timeInForce":"GTC"}`,
		"unknown type":        `{"clientId":"c1","symbol":"BTC-USD","side":"BUY","type":"STOP","price":"100","quantity":"1","timeInForce":"GTC"}`,
		"unknown timeInForce": `{"clientId":"c1","symbol":"BTC-USD","side":"BUY","type":"LIMIT","price":"100","quantity":"1","timeInForce":"XYZ"}`,
		"zero quantity":       `{"clientId":"c1","symbol":"BTC-USD","side":"BUY","type":"LIMIT","price":"100","quantity":"0","timeInForce":"GTC"}`,
		"limit without price": `{"clientId":"c1","symbol":"BTC-USD","side":"BUY","type":"LIMIT","quantity":"1","timeInForce":"GTC"}`,
		"malformed json":      `{"clientId":`,
	}
	for name, body := range cases {
		code, raw := s.do(t, http.MethodPost, "/orders", body, apiKey)
		if code != http.StatusBadRequest {
			t.Errorf("%s: expected 400, got %d (%s)", name, code, raw)
		}
	}
	if n := countRows(t, client, "SELECT count(*) FROM orders"); n != 0 {
		t.Fatalf("rejected requests must not be persisted, found %d orders", n)
	}
}

func TestAPI_CancelByAnotherClientIsForbidden(t *testing.T) {
	client := testutil.NewDB(t)
	s := startStack(t, client)

	sell := s.place(t, "c1", "SELL", 100, 5, "GTC")
	code, _ := s.do(t, http.MethodDelete, "/orders/"+sell.Id+"?client_id=c2", "", apiKey)
	if code != http.StatusForbidden {
		t.Fatalf("expected 403, got %d", code)
	}
}
func TestAPI_CompletedOrdersSurviveARestart(t *testing.T) {
	client := testutil.NewDB(t)

	s1 := startStack(t, client)
	sell := s1.place(t, "c1", "SELL", 100, 2, "GTC")
	buy := s1.place(t, "c2", "BUY", 100, 2, "GTC")
	s1.waitFor(t, sell.Id, types.OrderStatusCompleted, 2)
	s1.waitFor(t, buy.Id, types.OrderStatusCompleted, 2)
	s1.Stop()

	s2 := startStack(t, client) // fresh memory: both orders are over, so neither is restored

	if got := s2.get(t, sell.Id); got.Status != types.OrderStatusCompleted {
		t.Fatalf("completed order should still be readable, got %s", got.Status)
	}
	code, raw := s2.do(t, http.MethodGet, "/clients/c1/orders", "", apiKey)
	if code != http.StatusOK || !strings.Contains(string(raw), sell.Id) {
		t.Fatalf("client history should include the completed order: %d %s", code, raw)
	}
	if code, _ := s2.do(t, http.MethodGet, "/orders/not-a-uuid", "", apiKey); code != http.StatusNotFound {
		t.Fatalf("a malformed id should be 404, got %d", code)
	}
}

func waitUntil(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

func (s *stack) getBook(t *testing.T, path string) (int, types.BookSnapshot) {
	t.Helper()
	code, raw := s.do(t, http.MethodGet, path, "", apiKey)
	var b types.BookSnapshot
	if code == http.StatusOK {
		if err := json.Unmarshal(raw, &b); err != nil {
			t.Fatalf("decode book: %v (%s)", err, raw)
		}
	}
	return code, b
}

func (s *stack) getTrades(t *testing.T, path string) (int, []types.Trade) {
	t.Helper()
	code, raw := s.do(t, http.MethodGet, path, "", apiKey)
	var tr []types.Trade
	if code == http.StatusOK {
		if err := json.Unmarshal(raw, &tr); err != nil {
			t.Fatalf("decode trades: %v (%s)", err, raw)
		}
	}
	return code, tr
}

func TestAPI_BookSnapshot(t *testing.T) {
	s := startStack(t, testutil.NewDB(t))

	s.place(t, "c1", "SELL", 101, 2, "GTC")
	s.place(t, "c1", "SELL", 101, 3, "GTC")
	s.place(t, "c2", "BUY", 99, 4, "GTC")

	var book types.BookSnapshot
	waitUntil(t, "both sides of the book", func() bool {
		_, book = s.getBook(t, "/books/BTC-USD?depth=5")
		return len(book.Bids) == 1 && len(book.Asks) == 1
	})
	if !book.Asks[0].Quantity.Equal(decimal.NewFromInt(5)) || book.Asks[0].Orders != 2 {
		t.Fatalf("asks should aggregate to 5 across 2 orders: %+v", book.Asks)
	}
	if !book.Bids[0].Price.Equal(decimal.NewFromInt(99)) {
		t.Fatalf("unexpected best bid: %+v", book.Bids)
	}
}

func TestAPI_BookEdgeCases(t *testing.T) {
	s := startStack(t, testutil.NewDB(t))

	code, book := s.getBook(t, "/books/NOPE-USD")
	if code != http.StatusOK || book.Bids == nil || book.Asks == nil {
		t.Fatalf("unknown symbol should be 200 with empty arrays, got %d %+v", code, book)
	}
	for _, path := range []string{"/books/BTC-USD?depth=0", "/books/BTC-USD?depth=abc", "/books/BTC-USD?depth=-1"} {
		if code, _ := s.do(t, http.MethodGet, path, "", apiKey); code != http.StatusBadRequest {
			t.Errorf("%s: expected 400, got %d", path, code)
		}
	}
	if code, _ := s.do(t, http.MethodGet, "/books/BTC-USD", "", ""); code != http.StatusUnauthorized {
		t.Fatalf("book without a key: expected 401, got %d", code)
	}
}

func TestAPI_TradeEndpoints(t *testing.T) {
	s := startStack(t, testutil.NewDB(t))

	sell := s.place(t, "c1", "SELL", 100, 5, "GTC")
	buy := s.place(t, "c2", "BUY", 100, 2, "GTC")

	// trades are written by the database worker, so they appear a moment later
	var trades []types.Trade
	waitUntil(t, "the trade to be persisted", func() bool {
		_, trades = s.getTrades(t, "/trades?symbol=BTC-USD&limit=10")
		return len(trades) == 1
	})
	if trades[0].BuyOrderId != buy.Id || trades[0].SellOrderId != sell.Id ||
		!trades[0].Quantity.Equal(decimal.NewFromInt(2)) {
		t.Fatalf("unexpected trade: %+v", trades[0])
	}

	if code, got := s.getTrades(t, "/orders/"+sell.Id+"/trades"); code != http.StatusOK || len(got) != 1 {
		t.Fatalf("order trades: status %d, %d trades", code, len(got))
	}

	bad := map[string]int{
		"/trades":                          http.StatusBadRequest, // symbol missing
		"/trades?symbol=BTC-USD&limit=abc": http.StatusBadRequest,
		"/orders/not-a-uuid/trades":        http.StatusNotFound,
		"/orders/00000000-0000-0000-0000-000000000000/trades": http.StatusNotFound,
	}
	for path, want := range bad {
		if code, _ := s.do(t, http.MethodGet, path, "", apiKey); code != want {
			t.Errorf("%s: expected %d, got %d", path, want, code)
		}
	}
}

func TestAPI_ReadyPingsPostgres(t *testing.T) {
	s := startStack(t, testutil.NewDB(t))

	code, raw := s.do(t, http.MethodGet, "/ready", "", "") // needs no key
	if code != http.StatusOK || !strings.Contains(string(raw), `"postgres":"up"`) {
		t.Fatalf("/ready: %d %s", code, raw)
	}
	if code, _ := s.do(t, http.MethodGet, "/health", "", ""); code != http.StatusOK {
		t.Fatalf("/health: %d", code)
	}
}
func TestAPI_OrderJSONContract(t *testing.T) {
	s := startStack(t, testutil.NewDB(t))

	body := `{"clientId":"c1","symbol":"BTC-USD","side":"SELL","type":"LIMIT","price":"100","quantity":"5","timeInForce":"GTC"}`
	code, raw := s.do(t, http.MethodPost, "/orders", body, apiKey)
	if code != http.StatusCreated {
		t.Fatalf("place: %d %s", code, raw)
	}
	var placed map[string]any
	if err := json.Unmarshal(raw, &placed); err != nil {
		t.Fatal(err)
	}
	nested, ok := placed["order"].(map[string]any)
	if !ok || placed["message"] == nil {
		t.Fatalf("place response should be {order, message}: %s", raw)
	}

	code, raw = s.do(t, http.MethodGet, "/orders/"+nested["id"].(string), "", apiKey)
	if code != http.StatusOK {
		t.Fatalf("get: %d %s", code, raw)
	}
	var got map[string]any
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatal(err)
	}

	want := []string{"id", "clientId", "symbol", "side", "type", "status",
		"timeInForce", "price", "quantity", "filled", "createdAt", "updatedAt"}
	for _, k := range want {
		if _, ok := got[k]; !ok {
			t.Errorf("missing key %q in %s", k, raw)
		}
		if _, ok := nested[k]; !ok {
			t.Errorf("missing key %q in the place response's order", k)
		}
	}
	for _, m := range []map[string]any{got, nested} {
		for k := range m {
			if k[0] >= 'A' && k[0] <= 'Z' {
				t.Errorf("key %q should be camelCase", k)
			}
		}
	}
}
