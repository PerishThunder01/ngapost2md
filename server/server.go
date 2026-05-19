package server

import (
	"fmt"
	"log"
	"net/http"
	"os"
	"regexp"
	"strings"

	"github.com/ludoux/ngapost2md/config"
	"github.com/ludoux/ngapost2md/nga"
	"github.com/spf13/cast"
	"gopkg.in/ini.v1"
)

type ServeOpts struct {
	Port     int
	Password string
	NoUI     bool
}

func Start(opts ServeOpts) error {
	// 加载配置
	cfg, err := config.GetConfigAutoUpdate()
	if err != nil {
		return fmt.Errorf("无法加载配置文件: %v", err)
	}

	// 确定密码：命令行参数优先于配置文件
	password := opts.Password
	if password == "" {
		password = cfg.Section("server").Key("password").String()
	}
	if password == "" || strings.Contains(password, "MODIFY_ME") {
		return fmt.Errorf("Server 模式需要设置密码。请通过 --password 参数或 config.ini [server].password 设置密码。")
	}

	// 确定端口：命令行参数优先于配置文件
	port := opts.Port
	if port == 0 {
		port = cfg.Section("server").Key("port").MustInt(8080)
	}

	// 设置 nga 全局变量（与 CLI main.go 相同逻辑）
	var ngaPassportUid = cfg.Section("network").Key("ngaPassportUid").String()
	var ngaPassportCid = cfg.Section("network").Key("ngaPassportCid").String()
	var cookie strings.Builder
	cookie.WriteString("ngaPassportUid=" + ngaPassportUid + ";" + "ngaPassportCid=" + ngaPassportCid)
	nga.COOKIE = cookie.String()

	nga.BASE_URL = cfg.Section("network").Key("base_url").String()
	nga.UA = cfg.Section("network").Key("ua").String()

	// 核心配置项校验
	if ngaPassportUid == "" || strings.Contains(ngaPassportUid, "MODIFY_ME") {
		return fmt.Errorf("配置项配置错误: ngaPassportUid=%s", ngaPassportUid)
	}
	if ngaPassportCid == "" || strings.Contains(ngaPassportCid, "MODIFY_ME") {
		return fmt.Errorf("配置项配置错误: ngaPassportCid=%s", ngaPassportCid)
	}
	if nga.UA == "" || strings.Contains(nga.UA, "MODIFY_ME") {
		return fmt.Errorf("配置项配置错误: ua=%s", nga.UA)
	}

	nga.CFGFILE_THREAD_COUNT = cfg.Section("network").Key("thread").InInt(2, []int{1, 2, 3})
	nga.CFGFILE_PAGE_DOWNLOAD_LIMIT = cfg.Section("network").Key("page_download_limit").RangeInt(100, -1, 100)
	nga.CFGFILE_GET_IP_LOCATION = cfg.Section("post").Key("get_ip_location").MustBool()
	nga.CFGFILE_ENHANCE_ORI_REPLY = cfg.Section("post").Key("enhance_ori_reply").MustBool()
	nga.CFGFILE_ENHANCE_ORI_REPLY_ONLINE = cfg.Section("post").Key("enhance_ori_reply_online").MustBool()
	nga.CFGFILE_USE_LOCAL_SMILE_PIC = cfg.Section("post").Key("use_local_smile_pic").MustBool()
	nga.CFGFILE_LOCAL_SMILE_PIC_PATH = cfg.Section("post").Key("local_smile_pic_path").String()
	nga.CFGFILE_USE_TITLE_AS_FOLDER_NAME = cfg.Section("post").Key("use_title_as_folder_name").MustBool()
	nga.CFGFILE_USE_TITLE_AS_MD_FILE_NAME = cfg.Section("post").Key("use_title_as_md_file_name").MustBool()
	nga.CFGFILE_USE_NETWORK_MEDIA_URL = cfg.Section("post").Key("use_network_media_url").MustBool()
	nga.CFGFILE_ASSETS_PATH = cfg.Section("post").Key("assets_path").String()
	nga.CFGFILE_SPLIT_MD_FILE = cfg.Section("post").Key("split_md_file").RangeInt(-1, -1, 200)

	// 检查互斥项
	if nga.CFGFILE_ENHANCE_ORI_REPLY && nga.CFGFILE_THREAD_COUNT > 1 {
		return fmt.Errorf("配置项互斥检查失败，请检查 enhance_ori_reply 和 thread")
	}
	if nga.CFGFILE_ENHANCE_ORI_REPLY_ONLINE && !nga.CFGFILE_ENHANCE_ORI_REPLY {
		return fmt.Errorf("配置项互斥检查失败，请检查 enhance_ori_reply_online 和 enhance_ori_reply")
	}

	nga.Client = nga.NewNgaClient()

	// 创建 TaskManager 和 WebSocketHub
	hub := NewWebSocketHub()
	taskManager := NewTaskManager(hub)

	// 创建 ScheduleManager
	scheduleManager := NewScheduleManager(taskManager)
	if err := scheduleManager.Load(); err != nil {
		log.Println("加载定时任务失败:", err.Error(), "，将使用空的定时任务列表")
	}
	if err := scheduleManager.Start(); err != nil {
		log.Println("启动定时任务失败:", err.Error())
	}

	// 注册路由
	apiHandler := NewAPIHandler(taskManager, scheduleManager, cfg, password)

	mux := http.NewServeMux()

	// API 路由
	mux.HandleFunc("POST /api/download", apiHandler.HandleDownload)
	mux.HandleFunc("POST /api/update", apiHandler.HandleUpdate)
	mux.HandleFunc("GET /api/tasks", apiHandler.HandleTasks)
	mux.HandleFunc("DELETE /api/tasks/{tid}", apiHandler.HandleTaskCancel)
	mux.HandleFunc("GET /api/posts", apiHandler.HandlePosts)
	mux.HandleFunc("GET /api/schedules", apiHandler.HandleSchedulesGet)
	mux.HandleFunc("POST /api/schedules", apiHandler.HandleSchedulesCreate)
	mux.HandleFunc("PUT /api/schedules/{id}", apiHandler.HandleSchedulesUpdate)
	mux.HandleFunc("DELETE /api/schedules/{id}", apiHandler.HandleSchedulesDelete)
	mux.HandleFunc("GET /api/config", apiHandler.HandleConfigGet)
	mux.HandleFunc("PUT /api/config", apiHandler.HandleConfigPut)
	mux.HandleFunc("GET /ws", apiHandler.HandleWebSocket)

	// 前端路由
	if !opts.NoUI {
		mux.Handle("/", frontendHandler())
	}

	// 包装 Basic Auth 中间件
	handler := basicAuthMiddleware(mux, password)

	addr := fmt.Sprintf(":%d", port)
	log.Printf("ngapost2md Server 模式启动，监听 %s", addr)
	log.Printf("用户名: admin, 密码已设置")
	if !opts.NoUI {
		log.Printf("Web 前端已启用: http://localhost%s", addr)
	} else {
		log.Printf("仅 API 模式，前端已禁用")
	}

	return http.ListenAndServe(addr, handler)
}

func basicAuthMiddleware(handler http.Handler, password string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		username, pass, ok := r.BasicAuth()
		if !ok || username != "admin" || pass != password {
			w.Header().Set("WWW-Authenticate", "Basic realm=\"ngapost2md\"")
			http.Error(w, "Unauthorized", http.StatusUnauthorized)
			return
		}
		handler.ServeHTTP(w, r)
	})
}

// ReloadConfig 从 config.ini 重新加载配置并更新 nga 全局变量
func ReloadConfig() error {
	cfg, err := config.GetConfigAutoUpdate()
	if err != nil {
		return err
	}

	nga.CFGFILE_THREAD_COUNT = cfg.Section("network").Key("thread").InInt(2, []int{1, 2, 3})
	nga.CFGFILE_PAGE_DOWNLOAD_LIMIT = cfg.Section("network").Key("page_download_limit").RangeInt(100, -1, 100)
	nga.CFGFILE_GET_IP_LOCATION = cfg.Section("post").Key("get_ip_location").MustBool()
	nga.CFGFILE_ENHANCE_ORI_REPLY = cfg.Section("post").Key("enhance_ori_reply").MustBool()
	nga.CFGFILE_ENHANCE_ORI_REPLY_ONLINE = cfg.Section("post").Key("enhance_ori_reply_online").MustBool()
	nga.CFGFILE_USE_LOCAL_SMILE_PIC = cfg.Section("post").Key("use_local_smile_pic").MustBool()
	nga.CFGFILE_LOCAL_SMILE_PIC_PATH = cfg.Section("post").Key("local_smile_pic_path").String()
	nga.CFGFILE_USE_TITLE_AS_FOLDER_NAME = cfg.Section("post").Key("use_title_as_folder_name").MustBool()
	nga.CFGFILE_USE_TITLE_AS_MD_FILE_NAME = cfg.Section("post").Key("use_title_as_md_file_name").MustBool()
	nga.CFGFILE_USE_NETWORK_MEDIA_URL = cfg.Section("post").Key("use_network_media_url").MustBool()
	nga.CFGFILE_ASSETS_PATH = cfg.Section("post").Key("assets_path").String()
	nga.CFGFILE_SPLIT_MD_FILE = cfg.Section("post").Key("split_md_file").RangeInt(-1, -1, 200)

	// 重新创建 HTTP client（UA 可能变了）
	nga.Client = nga.NewNgaClient()

	return nil
}

// ScanAllPosts 扫描工作目录下所有已下载帖子
func ScanAllPosts() []PostInfo {
	entries, err := os.ReadDir(".")
	if err != nil {
		return nil
	}

	var posts []PostInfo
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		name := entry.Name()

		// 从文件夹名中提取 tid 和 authorId
		tid, authorId := parseTidFromFolderName(name)
		if tid == 0 {
			continue
		}

		// 检查是否有 process.ini
		processPath := fmt.Sprintf("%s/process.ini", name)
		if _, err := os.Stat(processPath); os.IsNotExist(err) {
			continue
		}

		// 读取 process.ini 获取元数据
		cfg, err := ini.Load(processPath)
		if err != nil {
			continue
		}
		maxPage := cfg.Section("local").Key("max_page").MustInt(0)
		maxFloor := cfg.Section("local").Key("max_floor").MustInt(-1)

		// 检查是否有 markdown 文件
		hasMarkdown := false
		mdFiles := []string{"post.md", fmt.Sprintf("%s.md", name)}
		for _, md := range mdFiles {
			if _, err := os.Stat(fmt.Sprintf("%s/%s", name, md)); err == nil {
				hasMarkdown = true
				break
			}
		}
		// 检查切分文件
		if !hasMarkdown {
			splitPath := fmt.Sprintf("%s/splitinfo.ini", name)
			if _, err := os.Stat(splitPath); err == nil {
				hasMarkdown = true
			}
		}

		// 尝试获取标题：从文件夹名中提取（去掉 tid 部分）
		title := extractTitleFromFolderName(name)

		posts = append(posts, PostInfo{
			Tid:         tid,
			AuthorId:    authorId,
			Title:       title,
			FolderName:  name,
			MaxPage:     maxPage,
			MaxFloor:    maxFloor,
			HasMarkdown: hasMarkdown,
		})
	}
	return posts
}

// parseTidFromFolderName 从文件夹名中解析 tid 和 authorId
// 支持格式: "123456", "123456-标题", "123456(2)", "123456(2)-标题"
func parseTidFromFolderName(name string) (int, int) {
	// 先尝试匹配带 authorId 的格式: tid(authorId) 或 tid(authorId)-title
	re := regexp.MustCompile(`^(\d+)\((\d+)\)`)
	matches := re.FindStringSubmatch(name)
	if len(matches) >= 3 {
		return cast.ToInt(matches[1]), cast.ToInt(matches[2])
	}

	// 简单格式: tid 或 tid-title
	re2 := regexp.MustCompile(`^(\d+)`)
	matches2 := re2.FindStringSubmatch(name)
	if len(matches2) >= 2 {
		return cast.ToInt(matches2[1]), 0
	}

	return 0, 0
}

// extractTitleFromFolderName 从文件夹名中提取标题部分
func extractTitleFromFolderName(name string) string {
	// tid-title 或 tid(authorId)-title 格式
	re := regexp.MustCompile(`^\d+(\(\d+\))?-(.+)$`)
	matches := re.FindStringSubmatch(name)
	if len(matches) >= 3 {
		return matches[2]
	}
	return name
}