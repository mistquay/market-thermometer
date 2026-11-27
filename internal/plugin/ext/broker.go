package ext

import (
	"context"
	"errors"
	"fmt"
)

// BrokerPosition 是外部交易通道插件返回的持仓（协议见 docs/plugin-protocol.md §5）。
type BrokerPosition struct {
	Code      string  `json:"code"`
	Qty       float64 `json:"qty"`
	Available float64 `json:"available"`
	Cost      float64 `json:"cost"`
}

// BrokerAccount 是外部交易通道插件返回的资金与持仓。
type BrokerAccount struct {
	Cash      float64          `json:"cash"`
	Frozen    float64          `json:"frozen"`
	Positions []BrokerPosition `json:"positions"`
}

// BrokerOrderReq 是下单请求（client_order_id 由核心生成保证幂等）。
type BrokerOrderReq struct {
	ClientOrderID string  `json:"client_order_id"`
	Code          string  `json:"code"`
	Side          string  `json:"side"` // buy | sell
	Qty           float64 `json:"qty"`
	PriceType     string  `json:"price_type"` // limit | market
	Price         float64 `json:"price"`
}

// BrokerOrder 是外部通道回报的订单状态。
type BrokerOrder struct {
	BrokerOrderID string  `json:"broker_order_id"`
	ClientOrderID string  `json:"client_order_id"`
	Status        string  `json:"status"`
	FilledQty     float64 `json:"filled_qty"`
	AvgPrice      float64 `json:"avg_price"`
}

// BrokerClient 是外部进程交易通道（KindBroker）的客户端接口。
// 凭证只存本机或环境变量；核心在调用 Place 前强制执行风控检查，插件不可绕过。
type BrokerClient struct {
	p *Process
}

// GetBroker 返回已登记的外部交易通道插件客户端（未注册时返回 nil）。
func GetBroker(name string) *BrokerClient {
	p := Get("broker/" + name)
	if p == nil {
		return nil
	}
	return &BrokerClient{p: p}
}

func (b *BrokerClient) Name() string { return b.p.manifest.Name }

func (b *BrokerClient) Account(ctx context.Context) (BrokerAccount, error) {
	var out BrokerAccount
	if err := b.p.Call(ctx, "broker.account", map[string]any{}, &out); err != nil {
		return out, unwrapRPC(err)
	}
	return out, nil
}

func (b *BrokerClient) Place(ctx context.Context, req BrokerOrderReq) (string, error) {
	if req.ClientOrderID == "" || req.Code == "" || (req.Side != "buy" && req.Side != "sell") || req.Qty <= 0 {
		return "", errors.New("broker.place: 缺少必填参数或方向/数量非法")
	}
	var res struct {
		BrokerOrderID string `json:"broker_order_id"`
	}
	if err := b.p.Call(ctx, "broker.place", req, &res); err != nil {
		return "", unwrapRPC(err)
	}
	if res.BrokerOrderID == "" {
		return "", fmt.Errorf("插件 %s 未返回 broker_order_id", b.Name())
	}
	return res.BrokerOrderID, nil
}

func (b *BrokerClient) Cancel(ctx context.Context, brokerOrderID string) error {
	if brokerOrderID == "" {
		return errors.New("broker.cancel: broker_order_id 为空")
	}
	return unwrapRPC(b.p.Call(ctx, "broker.cancel", map[string]string{"broker_order_id": brokerOrderID}, nil))
}

func (b *BrokerClient) Orders(ctx context.Context, since string) ([]BrokerOrder, error) {
	var res struct {
		Orders []BrokerOrder `json:"orders"`
	}
	if err := b.p.Call(ctx, "broker.orders", map[string]string{"since": since}, &res); err != nil {
		return nil, unwrapRPC(err)
	}
	return res.Orders, nil
}

func unwrapRPC(err error) error {
	var re *rpcError
	if errors.As(err, &re) {
		return fmt.Errorf("%s", re.Message)
	}
	return err
}
