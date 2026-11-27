package web

import (
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"

	"market-thermometer/internal/store"
)

func (s *Server) notesList(w http.ResponseWriter, r *http.Request) {
	target := strings.TrimSpace(r.URL.Query().Get("target"))
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	list, err := s.store.Notes(r.Context(), target, limit)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, list)
}

func (s *Server) noteSave(w http.ResponseWriter, r *http.Request) {
	var n store.ResearchNote
	if err := json.NewDecoder(r.Body).Decode(&n); err != nil {
		writeErr(w, http.StatusBadRequest, "bad request")
		return
	}
	n.ID = 0
	if idText := r.PathValue("id"); idText != "" {
		id, err := strconv.ParseInt(idText, 10, 64)
		if err != nil || id <= 0 {
			writeErr(w, http.StatusNotFound, "笔记不存在")
			return
		}
		n.ID = id
	}
	n.Title = strings.TrimSpace(n.Title)
	n.Target = strings.TrimSpace(n.Target)
	n.Tags = strings.TrimSpace(n.Tags)
	if n.Title == "" || len([]rune(n.Title)) > 120 {
		writeErr(w, http.StatusBadRequest, "标题须为 1–120 个字符")
		return
	}
	if len([]rune(n.Content)) > 20000 {
		writeErr(w, http.StatusBadRequest, "内容最多 20,000 个字符")
		return
	}
	id, err := s.store.SaveNote(r.Context(), n, s.now())
	if errors.Is(err, sql.ErrNoRows) {
		writeErr(w, http.StatusNotFound, "笔记不存在")
		return
	}
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	n.ID = id
	writeJSON(w, http.StatusOK, n)
}

func (s *Server) noteDelete(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r, "id")
	if !ok {
		writeErr(w, http.StatusNotFound, "笔记不存在")
		return
	}
	if err := s.store.DeleteNote(r.Context(), id); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}
