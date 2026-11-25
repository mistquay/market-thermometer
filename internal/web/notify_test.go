package web

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	_ "market-thermometer/internal/notify/dingtalk"
	_ "market-thermometer/internal/strategy/thermometer"
)

func TestNotifyChannelsAPI(t *testing.T) {
	h, st := newTestServerStore(t)
	const origin = "http://mt.local"
	w := do(h, "POST", "/api/auth/login", `{"password":"correct-horse"}`, nil, origin)
	cookies := w.Result().Cookies()

	const hook = "https://oapi.dingtalk.com/robot/send?access_token=abcdef0123456789"
	// 域名不在白名单 / 缺必填项：拒绝
	for _, body := range []string{
		`{"kind":"dingtalk","config":{"webhook":"https://evil.example.com/robot"}}`,
		`{"kind":"dingtalk","config":{}}`,
		`{"kind":"nope","config":{}}`,
	} {
		if w := do(h, "POST", "/api/notify/channels", body, cookies, origin); w.Code != 400 {
			t.Fatalf("%s: %d %s", body, w.Code, w.Body)
		}
	}
	w = do(h, "POST", "/api/notify/channels", `{"kind":"dingtalk","name":"群","config":{"webhook":"`+hook+`","secret":"SECabcdefgh1234"},"enabled":true}`, cookies, origin)
	if w.Code != 200 || strings.Contains(w.Body.String(), "abcdef0123456789") || strings.Contains(w.Body.String(), "SECabcdefgh1234") {
		t.Fatalf("create: %d %s", w.Code, w.Body)
	}
	var created struct {
		ID     int64             `json:"id"`
		Config map[string]string `json:"config"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &created)

	// 列表同样打码
	w = do(h, "GET", "/api/notify", "", cookies, "")
	if w.Code != 200 || strings.Contains(w.Body.String(), "abcdef0123456789") || !strings.Contains(w.Body.String(), `"signals":false`) {
		t.Fatalf("state: %d %s", w.Code, w.Body)
	}

	// 提交打码值：保持原值；改名、停用生效
	upd, _ := json.Marshal(map[string]any{"kind": "dingtalk", "name": "群2", "config": created.Config, "enabled": false})
	if w := do(h, "PUT", "/api/notify/channels/"+itoa(created.ID), string(upd), cookies, origin); w.Code != 200 {
		t.Fatalf("update: %d %s", w.Code, w.Body)
	}
	c, err := st.NotifyChannel(context.Background(), created.ID)
	if err != nil || c.Config["webhook"] != hook || c.Config["secret"] != "SECabcdefgh1234" || c.Name != "群2" || c.Enabled {
		t.Fatalf("stored: %+v %v", c, err)
	}

	// 开关
	if w := do(h, "POST", "/api/notify/settings", `{"signals":true,"weekly":true}`, cookies, origin); w.Code != 200 ||
		!strings.Contains(w.Body.String(), `"signals":true`) || !strings.Contains(w.Body.String(), `"weekly":true`) {
		t.Fatalf("settings: %d %s", w.Code, w.Body)
	}

	if w := do(h, "DELETE", "/api/notify/channels/"+itoa(created.ID), "", cookies, origin); w.Code != 200 {
		t.Fatalf("delete: %d %s", w.Code, w.Body)
	}
	if w := do(h, "POST", "/api/notify/channels/"+itoa(created.ID)+"/test", "", cookies, origin); w.Code != 404 {
		t.Fatalf("test deleted: %d %s", w.Code, w.Body)
	}
	if w := do(h, "PUT", "/api/notify/channels/abc", string(upd), cookies, origin); w.Code != 404 {
		t.Fatalf("bad id: %d %s", w.Code, w.Body)
	}
}

func itoa(n int64) string { b, _ := json.Marshal(n); return string(b) }
