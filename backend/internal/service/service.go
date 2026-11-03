package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strconv"
	"time"

	"baremetal-manager/backend/internal/adapter"
	"baremetal-manager/backend/internal/model"
	"baremetal-manager/backend/internal/repository"
	"github.com/hibiken/asynq"
	"github.com/redis/go-redis/v9"
)

// Service 组合数据存储、异步队列和 BMC 操作能力。
type Service struct {
	Store *repository.Store
	Queue *asynq.Client
	BMC   *adapter.BMC
}

// New 根据存储实例和 Redis 地址创建服务。
func New(store *repository.Store, redisURL string) (*Service, error) {
	opt, err := redis.ParseURL(redisURL)
	if err != nil {
		return nil, err
	}
	return &Service{Store: store, Queue: asynq.NewClient(asynq.RedisClientOpt{Addr: opt.Addr, Password: opt.Password, DB: opt.DB}), BMC: &adapter.BMC{}}, nil
}

// Submit 持久化设备操作并提交异步任务，拒绝未支持的安装和固件操作。
func (s *Service) Submit(user, project, asset uint, kind string, payload json.RawMessage) (*model.Operation, error) {
	if kind == "reinstall" || kind == "firmware" {
		return nil, errors.New("operation rejected: reinstall and firmware execution is not supported")
	}
	op := &model.Operation{UserID: user, ProjectID: project, AssetID: asset, Kind: kind, Payload: string(payload), Status: "queued"}
	if err := s.Store.DB.Create(op).Error; err != nil {
		return nil, err
	}
	data, _ := json.Marshal(map[string]uint{"id": op.ID})
	_, err := s.Queue.Enqueue(asynq.NewTask("bmc:operation", data), asynq.MaxRetry(3), asynq.Timeout(45*time.Second))
	if err != nil {
		s.Store.DB.Model(op).Updates(map[string]any{"status": "failed", "error": "queue unavailable"})
		return nil, err
	}
	s.Store.Audit(user, project, "operation.submit", fmt.Sprint(op.ID))
	return op, nil
}

// Handle 处理队列中的设备操作并更新执行状态。
func (s *Service) Handle(ctx context.Context, t *asynq.Task) error {
	var msg struct {
		ID uint `json:"id"`
	}
	if err := json.Unmarshal(t.Payload(), &msg); err != nil {
		return err
	}
	var op model.Operation
	if err := s.Store.DB.First(&op, msg.ID).Error; err != nil {
		return err
	}
	var asset model.Asset
	if err := s.Store.DB.Where("project_id = ?", op.ProjectID).First(&asset, op.AssetID).Error; err != nil {
		return err
	}
	secret, err := DecryptCredential(asset.BMCSecret)
	if err != nil {
		return err
	}
	asset.BMCSecret = secret
	s.Store.DB.Model(&op).Updates(map[string]any{"status": "running", "attempts": op.Attempts + 1})
	var payload struct {
		Action  string `json:"action"`
		ImageID uint   `json:"image_id"`
	}
	_ = json.Unmarshal([]byte(op.Payload), &payload)
	switch op.Kind {
	case "power":
		err = s.BMC.Power(ctx, asset, payload.Action)
	case "inspect":
		_, err = s.BMC.Inspect(ctx, asset)
	case "reinstall", "firmware":
		err = errors.New("provisioning integration not configured")
	default:
		err = errors.New("unsupported operation")
	}
	if err != nil {
		status := "retrying"
		retry, _ := asynq.GetRetryCount(ctx)
		if retry >= 3 {
			status = "failed"
		}
		s.Store.DB.Model(&op).Updates(map[string]any{"status": status, "error": err.Error()})
		return err
	}
	s.Store.DB.Model(&op).Updates(map[string]any{"status": "completed", "error": ""})
	s.Store.Audit(op.UserID, op.ProjectID, "operation.completed", strconv.FormatUint(uint64(op.ID), 10))
	return nil
}

// Worker 启动消费设备操作任务的队列工作进程。
func (s *Service) Worker(redisURL string) error {
	opt, err := redis.ParseURL(redisURL)
	if err != nil {
		return err
	}
	worker := asynq.NewServer(asynq.RedisClientOpt{Addr: opt.Addr, Password: opt.Password, DB: opt.DB}, asynq.Config{Concurrency: 5})
	return worker.Run(asynq.HandlerFunc(s.Handle))
}

// Scheduler 定期检查设备 BMC 连通性并为不可达设备生成告警。
func (s *Service) Scheduler(ctx context.Context) {
	tick := time.NewTicker(2 * time.Minute)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
			var assets []model.Asset
			s.Store.DB.Find(&assets)
			for _, asset := range assets {
				ctx2, cancel := context.WithTimeout(ctx, 15*time.Second)
				secret, err := DecryptCredential(asset.BMCSecret)
				if err == nil {
					asset.BMCSecret = secret
					_, err = s.BMC.Inspect(ctx2, asset)
				}
				cancel()
				if err != nil {
					s.Store.DB.Create(&model.Alert{ProjectID: asset.ProjectID, AssetID: asset.ID, Severity: "warning", Message: "BMC unreachable"})
				}
			}
		}
	}
}

// RequiredSecret 读取 JWT 签名密钥，长度不足时终止启动。
func RequiredSecret() string {
	v := os.Getenv("JWT_SECRET")
	if len(v) < 32 {
		panic("JWT_SECRET must contain at least 32 characters")
	}
	return v
}
