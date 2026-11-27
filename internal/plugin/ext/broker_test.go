package ext

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

func TestExternalBrokerPlugin(t *testing.T) {
	py, err := exec.LookPath("python")
	if err != nil {
		t.Skip("python not in PATH")
	}
	dir := t.TempDir()
	manifest := `{"name":"mock_broker","kind":"broker","version":"0.1.0","label":"Mock Broker"}`
	if err := os.WriteFile(filepath.Join(dir, "plugin.json"), []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}
	script := `import sys, json
for line in sys.stdin:
    req = json.loads(line)
    m = req["method"]
    if m == "initialize":
        res = {}
    elif m == "broker.account":
        res = {"cash": 1000.0, "frozen": 0.0, "positions": [{"code": "tok1", "qty": 10, "available": 10, "cost": 5.5}]}
    elif m == "broker.place":
        res = {"broker_order_id": "ord-" + req["params"]["client_order_id"]}
    elif m == "broker.cancel":
        res = {}
    elif m == "broker.orders":
        res = {"orders": [{"broker_order_id": "ord-c1", "client_order_id": "c1", "status": "filled", "filled_qty": 10, "avg_price": 0.55}]}
    elif m == "shutdown":
        sys.stdout.write(json.dumps({"jsonrpc": "2.0", "id": req["id"], "result": {}}) + "\n")
        sys.stdout.flush()
        break
    else:
        sys.stdout.write(json.dumps({"jsonrpc": "2.0", "id": req["id"], "error": {"code": -32601, "message": "unknown"}}) + "\n")
        sys.stdout.flush()
        continue
    sys.stdout.write(json.dumps({"jsonrpc": "2.0", "id": req["id"], "result": res}) + "\n")
    sys.stdout.flush()
`
	if err := os.WriteFile(filepath.Join(dir, "main.py"), []byte(script), 0o644); err != nil {
		t.Fatal(err)
	}
	p, err := Install(Spec{Dir: dir, Command: []string{py, "-u", "main.py"}, Timeout: 10 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { p.Close() })
	bc := GetBroker("mock_broker")
	if bc == nil {
		t.Fatal("GetBroker returned nil")
	}
	ctx := context.Background()
	acct, err := bc.Account(ctx)
	if err != nil || acct.Cash != 1000 || len(acct.Positions) != 1 {
		t.Fatalf("account %+v %v", acct, err)
	}
	oid, err := bc.Place(ctx, BrokerOrderReq{ClientOrderID: "c1", Code: "tok1", Side: "buy", Qty: 10, PriceType: "limit", Price: 0.55})
	if err != nil || oid != "ord-c1" {
		t.Fatalf("place %q %v", oid, err)
	}
	if err := bc.Cancel(ctx, oid); err != nil {
		t.Fatalf("cancel %v", err)
	}
	ords, err := bc.Orders(ctx, "")
	if err != nil || len(ords) != 1 || ords[0].Status != "filled" {
		t.Fatalf("orders %+v %v", ords, err)
	}
}
