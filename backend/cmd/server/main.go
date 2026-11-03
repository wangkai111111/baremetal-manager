package main

import (
	"context"
	"log"
	"net/http"
	"os"
	"time"

	"baremetal-manager/backend/internal/handler"
	"baremetal-manager/backend/internal/model"
	"baremetal-manager/backend/internal/repository"
	"baremetal-manager/backend/internal/service"
	"baremetal-manager/backend/seeds"
	"golang.org/x/crypto/bcrypt"
)

// main 初始化依赖及管理员账户，并按启动参数运行 API、任务工作进程或调度器。
func main() {
	dsn := os.Getenv("DATABASE_URL")
	redisURL := os.Getenv("REDIS_URL")
	if dsn == "" || redisURL == "" {
		log.Fatal("DATABASE_URL and REDIS_URL are required")
	}
	service.RequiredSecret()
	store, err := repository.Open(dsn)
	if err != nil {
		log.Fatal(err)
	}
	// 执行种子数据初始化（默认系统设置等）
	if err := seeds.SeedData(store.DB); err != nil {
		log.Printf("种子数据初始化警告: %v", err)
	}
	s, err := service.New(store, redisURL)
	if err != nil {
		log.Fatal(err)
	}
	defer s.Queue.Close()
	if username, password := os.Getenv("BOOTSTRAP_ADMIN_USER"), os.Getenv("BOOTSTRAP_ADMIN_PASSWORD"); username != "" && password != "" {
		var count int64
		store.DB.Model(&model.User{}).Count(&count)
		if count == 0 {
			hash, e := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
			if e != nil {
				log.Fatal(e)
			}
			store.DB.Create(&model.User{Username: username, PasswordHash: string(hash), Role: "admin"})
		}
	}
	mode := "api"
	if len(os.Args) > 1 {
		mode = os.Args[1]
	}
	switch mode {
	case "worker":
		log.Fatal(s.Worker(redisURL))
	case "scheduler":
		s.Scheduler(context.Background())
	case "api":
		h, e := handler.New(s)
		if e != nil {
			log.Fatal(e)
		}
		server := &http.Server{Addr: ":8000", Handler: h.Router(), ReadHeaderTimeout: 10 * time.Second}
		log.Fatal(server.ListenAndServe())
	default:
		log.Fatal("unknown mode")
	}
}
