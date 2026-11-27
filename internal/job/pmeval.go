package job

import (
	"context"

	ds "market-thermometer/internal/datasource"
	"market-thermometer/internal/pmeval"
	"market-thermometer/internal/store"
)

// EvaluatePMSignals 读取本地影子信号与历史盘口/现货快照，执行分层统计评估与盘口回放。
func EvaluatePMSignals(ctx context.Context, st *store.Store, fromMs, toMs int64, cfg pmeval.Config) (pmeval.Report, error) {
	rows, err := st.PMSignalsForEval(ctx, fromMs, toMs, 50_000)
	if err != nil {
		return pmeval.Report{}, err
	}
	sigs := make([]pmeval.Signal, 0, len(rows))
	for _, r := range rows {
		sigs = append(sigs, pmeval.Signal{
			ID: r.ID, T: r.T, Slug: r.Slug, Series: r.Series, Cond: r.Cond, Token0: r.Token0,
			Outcome: r.Outcome, EndT: r.EndT, P: r.P, Ask: r.Ask, AskSize: r.AskSize,
			FeeRate: r.FeeRate, FeeExp: r.FeeExp, MinSize: r.MinSize, Edge: r.Edge,
			K: r.K, KSrc: r.KSrc, SdBp: r.SdBp, Tau: r.Tau, Spot: r.Spot, Winner: r.Winner,
		})
	}
	feed := &pmeval.Feed{
		NextBook: func(token string, atMs, endMs int64) (ds.Book, bool, error) {
			snap, ok, err := st.PMBookAtOrAfter(ctx, token, atMs, endMs)
			if err != nil || !ok {
				return ds.Book{}, false, err
			}
			return ds.Book{Token: token, Bids: snap.Bids, Asks: snap.Asks}, true, nil
		},
		PrevBook: func(token string, atMs int64) (ds.Book, bool, error) {
			snap, ok, err := st.PMBookAtOrBefore(ctx, token, atMs)
			if err != nil || !ok {
				return ds.Book{}, false, err
			}
			return ds.Book{Token: token, Bids: snap.Bids, Asks: snap.Asks}, true, nil
		},
		BooksInRange: func(token string, fromMs, toMs int64) ([]ds.Book, error) {
			snaps, err := st.PMBooks(ctx, token, fromMs, toMs, 5000)
			if err != nil {
				return nil, err
			}
			out := make([]ds.Book, 0, len(snaps))
			for _, sn := range snaps {
				out = append(out, ds.Book{Token: token, Bids: sn.Bids, Asks: sn.Asks})
			}
			return out, nil
		},
		LastSpotT: func(symbol string, atMs int64) (int64, bool, error) {
			sp, ok, err := st.PMSpotBefore(ctx, symbol, atMs)
			if err != nil || !ok {
				return 0, false, err
			}
			return sp.T, true, nil
		},
	}
	return pmeval.Evaluate(sigs, cfg, feed), nil
}
