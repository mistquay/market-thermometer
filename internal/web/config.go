package web

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"

	"golang.org/x/crypto/bcrypt"

	"market-thermometer/internal/config"
)

// passwordHash 优先返回数据库里的管理员口令哈希，回退到启动 Options（兼容单测与旧方式）。
func (s *Server) passwordHash(ctx context.Context) (string, error) {
	if h, err := s.store.LoadAdminPasswordHash(ctx); err != nil {
		return "", err
	} else if h != "" {
		return h, nil
	}
	return s.opt.PasswordHash, nil
}

// authStatus 无鉴权接口：告知前端是否已初始化管理员口令以及当前浏览器是否已登录。
func (s *Server) authStatus(w http.ResponseWriter, r *http.Request) {
	h, err := s.passwordHash(r.Context())
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	authed := false
	if c, err := r.Cookie(sessionCookie); err == nil {
		if ok, err := s.store.ValidSession(r.Context(), c.Value, s.now()); err == nil && ok {
			authed = true
		}
	}
	writeJSON(w, http.StatusOK, map[string]bool{
		"initialized":   h != "",
		"authenticated": authed,
	})
}

// authSetup 首次零配置启动初始化管理员口令（仅当库内尚无口令时允许调用一次）。
func (s *Server) authSetup(w http.ResponseWriter, r *http.Request) {
	cur, err := s.passwordHash(r.Context())
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	if cur != "" {
		writeErr(w, http.StatusConflict, "管理员口令已初始化，请直接登录")
		return
	}
	var req struct {
		Password string `json:"password"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "bad request")
		return
	}
	pw := strings.TrimSpace(req.Password)
	if len(pw) < 8 {
		writeErr(w, http.StatusBadRequest, "口令长度至少 8 位")
		return
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(pw), bcrypt.DefaultCost)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	now := s.now()
	if err := s.store.SaveAdminPasswordHash(r.Context(), string(hash), now); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.audit(r.Context(), "auth.setup", "admin", map[string]any{"zeroConfig": true})
	token := newToken()
	if err := s.store.CreateSession(r.Context(), token, now, sessionTTL); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	http.SetCookie(w, &http.Cookie{
		Name: sessionCookie, Value: token, Path: "/", MaxAge: int(sessionTTL.Seconds()),
		HttpOnly: true, Secure: isHTTPS(r), SameSite: http.SameSiteLaxMode,
	})
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// authChangePassword 修改管理员口令（须校验当前口令）。
func (s *Server) authChangePassword(w http.ResponseWriter, r *http.Request) {
	var req struct {
		OldPassword string `json:"oldPassword"`
		NewPassword string `json:"newPassword"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "bad request")
		return
	}
	cur, err := s.passwordHash(r.Context())
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	if cur != "" && bcrypt.CompareHashAndPassword([]byte(cur), []byte(req.OldPassword)) != nil {
		writeErr(w, http.StatusUnauthorized, "当前口令不正确")
		return
	}
	np := strings.TrimSpace(req.NewPassword)
	if len(np) < 8 {
		writeErr(w, http.StatusBadRequest, "新口令长度至少 8 位")
		return
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(np), bcrypt.DefaultCost)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	if err := s.store.SaveAdminPasswordHash(r.Context(), string(hash), s.now()); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.audit(r.Context(), "auth.password", "admin", map[string]any{"changed": true})
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// configGet 返回数据库化的全部系统配置、内置默认值、来源与生效方式（白盒可观测）。
func (s *Server) configGet(w http.ResponseWriter, r *http.Request) {
	cfg, source, updatedAt, ok, err := s.store.LoadAppConfig(r.Context())
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	if !ok {
		if s.opt.Config != nil {
			cfg = s.opt.Config.Clone()
			source = s.opt.ConfigSource
		} else {
			cfg = config.Default()
			source = "default"
		}
	}
	mainPath, tickPath := s.store.Paths()
	writeJSON(w, http.StatusOK, map[string]any{
		"config":          cfg,
		"defaults":        config.Default(),
		"source":          source,
		"updatedAt":       updatedAt,
		"dbPath":          mainPath,
		"tickDbPath":      tickPath,
		"hotReloadKeys":   []string{"polymarket.strategy.minEdge", "polymarket.strategy.df"},
		"restartKeys":     []string{"listen", "httpProxy", "backfillStart", "sources", "stocks", "funds", "crypto", "polymarket.book", "polymarket.series"},
		"pluginsReadOnly": true,
	})
}

// configSave 将 Web 编辑的配置写入数据库，对支持热更新的参数立即生效，并写审计日志。
func (s *Server) configSave(w http.ResponseWriter, r *http.Request) {
	var next config.Config
	if err := json.NewDecoder(r.Body).Decode(&next); err != nil {
		writeErr(w, http.StatusBadRequest, "bad request")
		return
	}
	if err := next.Normalize(); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	prev, _, _, ok, err := s.store.LoadAppConfig(r.Context())
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	if !ok && s.opt.Config != nil {
		prev = s.opt.Config.Clone()
	}
	// 安全边界：外部进程插件的可执行路径与命令不能经由 Web 修改（防止任意命令执行）
	if prev != nil {
		next.Plugins = prev.Plugins
		next.DBPath = prev.DBPath
		next.TickDBPath = prev.TickDBPath
	} else {
		next.Plugins = nil
	}

	if err := s.store.SaveAppConfig(r.Context(), &next, "db", s.now()); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}

	var hotApplied []string
	if s.opt.PMShadow != nil {
		s.opt.PMShadow.UpdateParams(next.Polymarket.Strategy.MinEdge, next.Polymarket.Strategy.DF)
		hotApplied = append(hotApplied, "polymarket.strategy.minEdge", "polymarket.strategy.df")
	}

	restartRequired := prev == nil ||
		prev.Listen != next.Listen ||
		prev.HTTPProxy != next.HTTPProxy ||
		prev.BackfillStart != next.BackfillStart ||
		prev.Polymarket.Book.WS != next.Polymarket.Book.WS ||
		prev.Polymarket.Book.EveryMs != next.Polymarket.Book.EveryMs ||
		prev.Polymarket.Strategy.Shadow != next.Polymarket.Strategy.Shadow ||
		prev.Polymarket.Proxy != next.Polymarket.Proxy ||
		prev.Crypto.Proxy != next.Crypto.Proxy

	s.audit(r.Context(), "config.save", "app_config", map[string]any{
		"listen":          next.Listen,
		"pmWS":            next.Polymarket.Book.WS,
		"pmShadow":        next.Polymarket.Strategy.Shadow,
		"pmMinEdge":       next.Polymarket.Strategy.MinEdge,
		"pmDF":            next.Polymarket.Strategy.DF,
		"hotApplied":      hotApplied,
		"restartRequired": restartRequired,
	})
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":              true,
		"config":          next,
		"hotApplied":      hotApplied,
		"restartRequired": restartRequired,
	})
}

// configReset 将数据库配置重置为内置零配置默认值（保留口令哈希与外部插件列表）。
func (s *Server) configReset(w http.ResponseWriter, r *http.Request) {
	def := config.Default()
	if prev, _, _, ok, err := s.store.LoadAppConfig(r.Context()); err == nil && ok {
		def.Plugins = prev.Plugins
		def.DBPath = prev.DBPath
		def.TickDBPath = prev.TickDBPath
	}
	if err := s.store.SaveAppConfig(r.Context(), def, "default", s.now()); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	if s.opt.PMShadow != nil {
		s.opt.PMShadow.UpdateParams(def.Polymarket.Strategy.MinEdge, def.Polymarket.Strategy.DF)
	}
	s.audit(r.Context(), "config.reset", "app_config", map[string]any{"resetTo": "default"})
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "config": def, "restartRequired": true})
}
