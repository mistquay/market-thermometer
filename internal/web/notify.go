package web

import (
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"

	ds "market-thermometer/internal/datasource"
	"market-thermometer/internal/job"
	"market-thermometer/internal/notify"
	"market-thermometer/internal/store"
)

// 推送设置：渠道的敏感字段在响应中一律打码；提交打码值或空值表示保持原值。

type channelOut struct {
	store.NotifyChannel
	KindLabel string `json:"kindLabel"`
}

func maskChannel(c store.NotifyChannel) channelOut {
	k, ok := notify.Lookup(c.Kind)
	if !ok { // 插件已移除：不知道哪些字段敏感，全部隐藏
		c.Config = map[string]string{}
		return channelOut{c, c.Kind}
	}
	c.Config = k.Mask(c.Config)
	return channelOut{c, k.Label}
}

func (s *Server) notifyState(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	chs, err := s.store.NotifyChannels(ctx)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	out := make([]channelOut, 0, len(chs))
	for _, c := range chs {
		out = append(out, maskChannel(c))
	}
	logs, err := s.store.RecentNotifyLogs(ctx, 20)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	settings := map[string]string{}
	for _, k := range []string{job.SettingSignals, job.SettingSignalsSince, job.SettingWeekly} {
		v, err := s.store.Setting(ctx, k)
		if err != nil {
			writeErr(w, http.StatusInternalServerError, err.Error())
			return
		}
		settings[k] = v
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"kinds":    notify.Kinds(),
		"channels": out,
		"logs":     logs,
		"signals":  settings[job.SettingSignals] == "on",
		"since":    settings[job.SettingSignalsSince],
		"weekly":   settings[job.SettingWeekly] == "on",
	})
}

type channelReq struct {
	Kind    string            `json:"kind"`
	Name    string            `json:"name"`
	Config  map[string]string `json:"config"`
	Enabled bool              `json:"enabled"`
}

// saveChannel 处理新增（POST）与修改（PUT /{id}）。
func (s *Server) saveChannel(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	var req channelReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "bad request")
		return
	}
	var old store.NotifyChannel
	if idText := r.PathValue("id"); idText != "" {
		id, err := strconv.ParseInt(idText, 10, 64)
		if err == nil {
			old, err = s.store.NotifyChannel(ctx, id)
		}
		if errors.Is(err, sql.ErrNoRows) || id <= 0 {
			writeErr(w, http.StatusNotFound, "渠道不存在")
			return
		}
		if err != nil {
			writeErr(w, http.StatusInternalServerError, err.Error())
			return
		}
		if req.Kind != old.Kind {
			old.Config = nil // 换了渠道类型，旧密钥不能沿用
		}
	}
	k, ok := notify.Lookup(req.Kind)
	if !ok {
		writeErr(w, http.StatusBadRequest, "未知的推送渠道")
		return
	}
	cfg := k.Merge(old.Config, req.Config)
	if _, err := notify.Build(req.Kind, cfg, notify.Env{}); err != nil { // 校验必填项与 webhook 域名
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	name := strings.TrimSpace(req.Name)
	if name == "" {
		name = k.Label
	}
	id, err := s.store.SaveNotifyChannel(ctx, store.NotifyChannel{ID: old.ID, Kind: req.Kind, Name: name, Config: cfg, Enabled: req.Enabled})
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	c, err := s.store.NotifyChannel(ctx, id)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, maskChannel(c))
}

func (s *Server) channelByPath(w http.ResponseWriter, r *http.Request) (store.NotifyChannel, bool) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	var c store.NotifyChannel
	if err == nil {
		c, err = s.store.NotifyChannel(r.Context(), id)
	}
	switch {
	case err == nil:
		return c, true
	case errors.Is(err, sql.ErrNoRows), errors.Is(err, strconv.ErrSyntax):
		writeErr(w, http.StatusNotFound, "渠道不存在")
	default:
		writeErr(w, http.StatusInternalServerError, err.Error())
	}
	return c, false
}

func (s *Server) deleteChannel(w http.ResponseWriter, r *http.Request) {
	c, ok := s.channelByPath(w, r)
	if !ok {
		return
	}
	if err := s.store.DeleteNotifyChannel(r.Context(), c.ID); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// testChannel 向已保存的渠道发送一条测试消息（不受推送开关限制）。
func (s *Server) testChannel(w http.ResponseWriter, r *http.Request) {
	c, ok := s.channelByPath(w, r)
	if !ok {
		return
	}
	msg := notify.Message{
		Title: "【A股温度计 · 测试消息】",
		Lines: []string{"这是一条测试消息，用于确认推送渠道配置正确。", "发送时间：" + s.now().In(ds.Shanghai).Format("2006-01-02 15:04:05")},
	}
	res := s.pusher.SendTo(r.Context(), []store.NotifyChannel{c}, msg)[0]
	writeJSON(w, http.StatusOK, res)
}

func (s *Server) saveNotifySettings(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	var req struct {
		Signals *bool `json:"signals"`
		Weekly  *bool `json:"weekly"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "bad request")
		return
	}
	if req.Signals != nil {
		if err := s.pusher.SetSignals(ctx, *req.Signals); err != nil {
			writeErr(w, http.StatusInternalServerError, err.Error())
			return
		}
	}
	if req.Weekly != nil {
		v := ""
		if *req.Weekly {
			v = "on"
		}
		if err := s.store.SetSetting(ctx, job.SettingWeekly, v); err != nil {
			writeErr(w, http.StatusInternalServerError, err.Error())
			return
		}
	}
	s.notifyState(w, r)
}
