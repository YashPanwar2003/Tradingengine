package order

import (
	"context"
	"errors"
	"fmt"
	"log"
	"time"

	"tradingengine/pkg/types"
)

// Persister is what PersistentStore needs from the database layer.
// *db.DBStorage satisfies it.
type Persister interface {
	InsertOrder(ctx context.Context, o *types.Order) error
	UpdateOrderFill(ctx context.Context, o *types.Order) error
	RecordFill(ctx context.Context, incoming, resting *types.Order, trade *types.Trade) error
	GetOrderByID(ctx context.Context, id string) (*types.Order, error)
	ListOrdersByClient(ctx context.Context, clientID string, limit int) ([]*types.Order, error)
	FindByClientOrderID(ctx context.Context, clientID, clientOrderID string) (*types.Order, error)
}

type fillJob struct {
	incoming types.Order
	resting  types.Order
	trade    types.Trade
}

// job is one unit of work for the DB worker: a single order update OR a fill.
// Both hold snapshots, because the matcher keeps mutating the live orders.
type job struct {
	update *types.Order
	fill   *fillJob
}

type PersistentStore struct {
	Store // embedded: reads pass straight through to memory
	db    Persister
	jobs  chan job
	stop  chan struct{}
	done  chan struct{}
}

type DuplicateError struct{ Existing *types.Order }

func (e *DuplicateError) Error() string { return "duplicate clientOrderId" }

func NewPersistentStore(mem Store, db Persister, buffer int) *PersistentStore {
	return &PersistentStore{
		Store: mem,
		db:    db,
		jobs:  make(chan job, buffer),
		stop:  make(chan struct{}),
		done:  make(chan struct{}),
	}
}

// Save writes the new order to Postgres first, then to memory.
func (p *PersistentStore) Save(o *types.Order) error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := p.db.InsertOrder(ctx, o); err != nil {
		if errors.Is(err, types.ErrDuplicateClientOrderId) {
			return p.duplicate(ctx, o)
		}
		return fmt.Errorf("persist new order: %w", err)
	}
	return p.Store.Save(o)
}

func (p *PersistentStore) duplicate(ctx context.Context, o *types.Order) error {
	existing, err := p.db.FindByClientOrderID(ctx, o.ClientId, o.ClientOrderId)
	if err != nil {
		return fmt.Errorf("load the order that already uses this clientOrderId: %w", err)
	}
	if fresh, err := p.Store.GetById(existing.Id); err == nil {
		existing = fresh // memory is ahead of the asynchronous database writes
	}
	return &DuplicateError{Existing: existing}
}

// Update handles cancels and rejects: memory now, one queued DB write.
func (p *PersistentStore) Update(o *types.Order) error {
	if err := p.Store.Update(o); err != nil {
		return err
	}
	snap := *o
	p.jobs <- job{update: &snap}
	return nil
}

// RecordFill updates memory for both orders and queues ONE transactional job.
func (p *PersistentStore) RecordFill(incoming, resting *types.Order, trade *types.Trade) error {
	if err := p.Store.RecordFill(incoming, resting, trade); err != nil {
		return err
	}
	p.jobs <- job{fill: &fillJob{incoming: *incoming, resting: *resting, trade: *trade}}
	return nil
}

// Run is the DB worker. Start it with: go persistentStore.Run()
func (p *PersistentStore) Run() {
	defer close(p.done)
	for {
		select {
		case j := <-p.jobs:
			p.process(j)
		case <-p.stop:
			for { // drain what is still queued, then exit
				select {
				case j := <-p.jobs:
					p.process(j)
				default:
					return
				}
			}
		}
	}
}

// Close stops the worker after it has flushed pending jobs. Call it once.
func (p *PersistentStore) Close() {
	close(p.stop)
	<-p.done
}

func (p *PersistentStore) process(j job) {
	var err error
	for attempt := 1; attempt <= 3; attempt++ {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		if j.fill != nil {
			err = p.db.RecordFill(ctx, &j.fill.incoming, &j.fill.resting, &j.fill.trade)
		} else {
			err = p.db.UpdateOrderFill(ctx, j.update)
		}
		cancel()
		if err == nil {
			return
		}
		time.Sleep(time.Duration(attempt) * 200 * time.Millisecond)
	}

	if j.fill != nil {
		log.Printf("PERSISTENCE FAILED fill trade=%s incoming=%s resting=%s: %v",
			j.fill.trade.Id, j.fill.incoming.Id, j.fill.resting.Id, err)
		return
	}
	log.Printf("PERSISTENCE FAILED order=%s status=%s filled=%s: %v",
		j.update.Id, j.update.Status, j.update.Filled.String(), err)
}

// GetById serves live orders from memory and falls back to Postgres for
// orders that memory no longer knows (completed or cancelled before a restart).
func (p *PersistentStore) GetById(id string) (*types.Order, error) {
	o, err := p.Store.GetById(id)
	if !errors.Is(err, types.ErrOrderNotFound) {
		return o, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return p.db.GetOrderByID(ctx, id)
}

// GetByClient returns the client's recent history from Postgres, with memory
// overlaid on top: memory is fresher, because database writes are asynchronous.
func (p *PersistentStore) GetByClient(clientID string) ([]*types.Order, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	stored, err := p.db.ListOrdersByClient(ctx, clientID, 100)
	if err != nil {
		log.Printf("client history from postgres failed, serving memory only: %v", err)
		return p.Store.GetByClient(clientID)
	}
	for i, o := range stored {
		if fresh, err := p.Store.GetById(o.Id); err == nil {
			stored[i] = fresh
		}
	}
	return stored, nil
}
