package engine

import (
	"context"
	"github.com/HydroProtocol/hydro-sdk-backend/common"
	"sync"
)

type Engine struct {
	marketHandlerMap map[string]*MarketHandler

	// Wait for all queue handler exit gracefully
	Wg sync.WaitGroup

	// global ctx, if this ctx is canceled, queue handlers should exit in a short time.
	ctx context.Context

	dbHandler                  *DBHandler
	orderBookSnapshotHandler   *OrderBookSnapshotHandler
	orderBookActivitiesHandler *OrderBookActivitiesHandler
}

func NewEngine(ctx context.Context) *Engine {
	engine := &Engine{
		ctx:              ctx,
		marketHandlerMap: make(map[string]*MarketHandler),
		Wg:               sync.WaitGroup{},
	}

	return engine
}

func (e *Engine) RegisterDBHandler(handler DBHandler) {
	e.dbHandler = &handler
}
func (e *Engine) RegisterOrderBookSnapshotHandler(handler OrderBookSnapshotHandler) {
	e.orderBookSnapshotHandler = &handler
}
func (e *Engine) RegisterOrderBookActivitiesHandler(handler OrderBookActivitiesHandler) {
	e.orderBookActivitiesHandler = &handler
}

type DBHandler interface {
	Update(matchResult common.MatchResult) sync.WaitGroup
}
type OrderBookSnapshotHandler interface {
	Update(key string, snapshot *common.SnapshotV2) sync.WaitGroup
}
type OrderBookActivitiesHandler interface {
	Update(webSocketMessages []common.WebSocketMessage) sync.WaitGroup
}

func (e *Engine) HandleNewOrder(order *common.MemoryOrder) (matchResult common.MatchResult, hasMatch bool) {
	// find or create marketHandler if not exist yet
	if _, exist := e.marketHandlerMap[order.MarketID]; !exist {
		marketHandler, err := NewMarketHandler(e.ctx, order.MarketID)
		if err != nil {
			panic(err)
		}

		e.marketHandlerMap[order.MarketID] = marketHandler
	}

	// feed the handler with this new order
	handler, _ := e.marketHandlerMap[order.MarketID]
	matchResult, hasMatch = handler.handleNewOrder(order)

	if e.dbHandler != nil {
		(*e.dbHandler).Update(matchResult)
	}

	e.triggerOrderBookSnapshotHandlerIfNotNil(handler)

	if e.orderBookActivitiesHandler != nil {
		(*e.orderBookActivitiesHandler).Update(matchResult.OrderBookActivities)
	}

	return
}

func (e *Engine) ReInsertOrder(order *common.MemoryOrder) (msg *common.WebSocketMessage) {
	if _, exist := e.marketHandlerMap[order.MarketID]; !exist {
		marketHandler, err := NewMarketHandler(e.ctx, order.MarketID)
		if err != nil {
			panic(err)
		}

		e.marketHandlerMap[order.MarketID] = marketHandler
	}

	handler, _ := e.marketHandlerMap[order.MarketID]
	event := handler.orderbook.InsertOrder(order)

	e.triggerOrderBookSnapshotHandlerIfNotNil(handler)

	if event == nil {
		return
	} else {
		msg := common.OrderBookChangeMessage(handler.market, handler.orderbook.Sequence, event.Side, event.Price, event.Amount)
		return &msg
	}
}

func (e *Engine) HandleCancelOrder(order *common.MemoryOrder) (msg *common.WebSocketMessage, success bool) {
	handler, _ := e.marketHandlerMap[order.MarketID]

	event := handler.handleCancelOrder(order)
	if event == nil {
		return
	} else {
		e.triggerOrderBookSnapshotHandlerIfNotNil(handler)

		msg := common.OrderBookChangeMessage(handler.market, handler.orderbook.Sequence, event.Side, event.Price, event.Amount)
		return &msg, true
	}
}

func (e *Engine) triggerOrderBookSnapshotHandlerIfNotNil(handler *MarketHandler) {
	if e.orderBookSnapshotHandler != nil {
		snapshot := handler.orderbook.SnapshotV2()
		snapshot.Sequence = handler.orderbook.Sequence

		snapshotKey := common.GetMarketOrderbookSnapshotV2Key(handler.market)

		(*e.orderBookSnapshotHandler).Update(snapshotKey, snapshot)
	}
}
