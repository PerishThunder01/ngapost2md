package server

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/ludoux/ngapost2md/config"
	"github.com/spf13/cast"
	"gopkg.in/ini.v1"
)

type APIHandler struct {
	taskManager      *TaskManager
	scheduleManager  *ScheduleManager
	cfg              *ini.File
	password         string
}

func NewAPIHandler(taskManager *TaskManager, scheduleManager *ScheduleManager, cfg *ini.File, password string) *APIHandler {
	return &APIHandler{
		taskManager:     taskManager,
		scheduleManager: scheduleManager,
		cfg:             cfg,
		password:        password,
	}
}

func writeJSON(w http.ResponseWriter, statusCode int, data interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(statusCode)
	json.NewEncoder(w).Encode(data)
}

func writeError(w http.ResponseWriter, statusCode int, message string) {
	writeJSON(w, statusCode, map[string]string{"error": message})
}

func readBody(r *http.Request) ([]byte, error) {
	body, err := io.ReadAll(r.Body)
	if err != nil {
		return nil, fmt.Errorf("读取请求体失败: %v", err)
	}
	return body, nil
}

// POST /api/download
func (h *APIHandler) HandleDownload(w http.ResponseWriter, r *http.Request) {
	body, err := readBody(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	var req struct {
		Tid      int `json:"tid"`
		AuthorId int `json:"authorId"`
	}
	if err := json.Unmarshal(body, &req); err != nil {
		writeError(w, http.StatusBadRequest, "无效的请求体: "+err.Error())
		return
	}

	if req.Tid == 0 {
		writeError(w, http.StatusBadRequest, "tid 不能为空或0")
		return
	}

	task, err := h.taskManager.EnqueueDownload(req.Tid, req.AuthorId)
	if err != nil {
		if strings.Contains(err.Error(), "已在队列") || strings.Contains(err.Error(), "正在执行") {
			writeError(w, http.StatusConflict, err.Error())
		} else {
			writeError(w, http.StatusInternalServerError, err.Error())
		}
		return
	}

	writeJSON(w, http.StatusAccepted, task)
}

// POST /api/update
func (h *APIHandler) HandleUpdate(w http.ResponseWriter, r *http.Request) {
	body, err := readBody(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	var req struct {
		Tid int `json:"tid"`
	}
	if err := json.Unmarshal(body, &req); err != nil {
		writeError(w, http.StatusBadRequest, "无效的请求体: "+err.Error())
		return
	}

	if req.Tid == 0 {
		writeError(w, http.StatusBadRequest, "tid 不能为空或0")
		return
	}

	task, err := h.taskManager.EnqueueUpdate(req.Tid)
	if err != nil {
		if strings.Contains(err.Error(), "已在队列") || strings.Contains(err.Error(), "正在执行") {
			writeError(w, http.StatusConflict, err.Error())
		} else {
			writeError(w, http.StatusInternalServerError, err.Error())
		}
		return
	}

	writeJSON(w, http.StatusAccepted, task)
}

// GET /api/tasks
func (h *APIHandler) HandleTasks(w http.ResponseWriter, r *http.Request) {
	tasks := h.taskManager.GetAllTasks()
	writeJSON(w, http.StatusOK, tasks)
}

// DELETE /api/tasks/{tid}
func (h *APIHandler) HandleTaskCancel(w http.ResponseWriter, r *http.Request) {
	tidStr := r.PathValue("tid")
	tid := cast.ToInt(tidStr)
	if tid == 0 {
		writeError(w, http.StatusBadRequest, "无效的 tid")
		return
	}

	if err := h.taskManager.CancelTask(tid); err != nil {
		writeError(w, http.StatusNotFound, err.Error())
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{"message": "任务已取消"})
}

// GET /api/posts
func (h *APIHandler) HandlePosts(w http.ResponseWriter, r *http.Request) {
	posts := ScanAllPosts()
	writeJSON(w, http.StatusOK, posts)
}

// GET /api/schedules
func (h *APIHandler) HandleSchedulesGet(w http.ResponseWriter, r *http.Request) {
	schedules := h.scheduleManager.GetAllSchedules()
	writeJSON(w, http.StatusOK, schedules)
}

// POST /api/schedules
func (h *APIHandler) HandleSchedulesCreate(w http.ResponseWriter, r *http.Request) {
	body, err := readBody(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	var req struct {
		Tid      int    `json:"tid"`
		AuthorId int    `json:"authorId"`
		Cron     string `json:"cron"`
	}
	if err := json.Unmarshal(body, &req); err != nil {
		writeError(w, http.StatusBadRequest, "无效的请求体: "+err.Error())
		return
	}

	if req.Tid == 0 {
		writeError(w, http.StatusBadRequest, "tid 不能为空或0")
		return
	}
	if req.Cron == "" {
		writeError(w, http.StatusBadRequest, "cron 表达式不能为空")
		return
	}

	schedule, err := h.scheduleManager.AddSchedule(req.Tid, req.AuthorId, req.Cron)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	writeJSON(w, http.StatusCreated, schedule)
}

// PUT /api/schedules/{id}
func (h *APIHandler) HandleSchedulesUpdate(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	body, err := readBody(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	var req struct {
		Cron    string `json:"cron,omitempty"`
		Enabled bool   `json:"enabled"`
	}
	if err := json.Unmarshal(body, &req); err != nil {
		writeError(w, http.StatusBadRequest, "无效的请求体: "+err.Error())
		return
	}

	schedule, err := h.scheduleManager.UpdateSchedule(id, req.Cron, req.Enabled)
	if err != nil {
		writeError(w, http.StatusNotFound, err.Error())
		return
	}

	writeJSON(w, http.StatusOK, schedule)
}

// DELETE /api/schedules/{id}
func (h *APIHandler) HandleSchedulesDelete(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")

	if err := h.scheduleManager.DeleteSchedule(id); err != nil {
		writeError(w, http.StatusNotFound, err.Error())
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{"message": "定时任务已删除"})
}

// GET /api/config
func (h *APIHandler) HandleConfigGet(w http.ResponseWriter, r *http.Request) {
	cfg, err := config.GetConfigAutoUpdate()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	result := map[string]map[string]string{}
	for _, section := range cfg.Sections() {
		secName := section.Name()
		if secName == "DEFAULT" {
			continue
		}
		// 不暴露 server password
		secMap := map[string]string{}
		for _, key := range section.Keys() {
			if secName == "server" && key.Name() == "password" {
				continue
			}
			secMap[key.Name()] = key.Value()
		}
		result[secName] = secMap
	}

	writeJSON(w, http.StatusOK, result)
}

// PUT /api/config
func (h *APIHandler) HandleConfigPut(w http.ResponseWriter, r *http.Request) {
	body, err := readBody(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	var updates map[string]map[string]string
	if err := json.Unmarshal(body, &updates); err != nil {
		writeError(w, http.StatusBadRequest, "无效的请求体: "+err.Error())
		return
	}

	// 不允许通过 API 修改 server password
	if serverUpdates, ok := updates["server"]; ok {
		if _, exists := serverUpdates["password"]; exists {
			writeError(w, http.StatusBadRequest, "不允许通过 API 修改 server.password，请通过命令行参数或手动编辑 config.ini 修改")
			return
		}
	}

	cfg, err := config.GetConfigAutoUpdate()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	// 应用更新
	for secName, secUpdates := range updates {
		if !cfg.HasSection(secName) {
			cfg.NewSection(secName)
		}
		for key, value := range secUpdates {
			cfg.Section(secName).Key(key).SetValue(value)
		}
	}

	// 保存
	if err := cfg.SaveTo("config.ini"); err != nil {
		writeError(w, http.StatusInternalServerError, "保存配置文件失败: "+err.Error())
		return
	}

	// 重新加载配置
	if err := ReloadConfig(); err != nil {
		writeError(w, http.StatusInternalServerError, "重新加载配置失败: "+err.Error())
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{"message": "配置已保存并生效"})
}

// GET /ws
func (h *APIHandler) HandleWebSocket(w http.ResponseWriter, r *http.Request) {
	HandleWebSocket(w, r, h.password, h.taskManager.hub)
}