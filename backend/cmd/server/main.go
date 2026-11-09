package main

import (
	"context"
	"errors"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"baremetal-manager/backend/internal/platform"
	"baremetal-manager/backend/migrations"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

// main 将迁移与运行分离，API 与工作节点绝不执行 AutoMigrate。
func main() {
	if err := run(); err != nil {
		log.Fatal(err)
	}
}

func run() error {
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		return errors.New("DATABASE_URL is required")
	}
	db, err := gorm.Open(postgres.Open(dsn), &gorm.Config{})
	if err != nil {
		return err
	}
	sqlDB, err := db.DB()
	if err != nil {
		return err
	}
	defer sqlDB.Close()
	sqlDB.SetMaxOpenConns(20)
	sqlDB.SetMaxIdleConns(5)
	sqlDB.SetConnMaxLifetime(30 * time.Minute)
	mode := "api"
	if len(os.Args) > 1 {
		mode = os.Args[1]
	}
	if mode == "migrate" {
		if err := migrations.Apply(db); err != nil {
			return err
		}
		if err := migrations.Bootstrap(db); err != nil {
			return err
		}
		if err := migrations.GrantApplicationRole(db); err != nil {
			return err
		}
		log.Print("target schema migration and bootstrap completed")
		return nil
	}
	if err := migrations.Verify(db); err != nil {
		return err
	}
	app, err := platform.New(db)
	if err != nil {
		return err
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	switch mode {
	case "worker":
		app.Worker(ctx)
	case "scheduler":
		app.Scheduler(ctx)
	case "api":
		server := &http.Server{Addr: ":8000", Handler: app.Router(), ReadHeaderTimeout: 10 * time.Second, IdleTimeout: 90 * time.Second}
		done := make(chan error, 1)
		go func() { done <- server.ListenAndServe() }()
		select {
		case err := <-done:
			if !errors.Is(err, http.ErrServerClosed) {
				return err
			}
		case <-ctx.Done():
			shutdown, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()
			return server.Shutdown(shutdown)
		}
	default:
		return errors.New("unknown mode: expected api, worker, scheduler or migrate")
	}
	return nil
}
