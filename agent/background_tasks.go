package agent

import (
	"context"
	"crypto/rand"
	"sync"
	"time"
)

// BackgroundTaskManager 后台任务管理器
type BackgroundTaskManager struct {
	tasks map[string]*BackgroundTask
	mu    sync.RWMutex
}

// NewBackgroundTaskManager 创建后台任务管理器
func NewBackgroundTaskManager() *BackgroundTaskManager {
	return &BackgroundTaskManager{
		tasks: make(map[string]*BackgroundTask),
	}
}

// BackgroundTask 后台任务
type BackgroundTask struct {
	ID          string
	Command     string
	Description string
	StartTime   time.Time
	Status      string // "running", "completed", "failed", "cancelled"
	Done        <-chan struct{}
	OnComplete  func(ToolResult, error)
}

// StartTask 启动后台任务
func (m *BackgroundTaskManager) StartTask(
	ctx context.Context,
	command string,
	description string,
	timeout time.Duration,
) (string, error) {
	taskID := generateTaskID("bg")
	taskCtx, cancel := context.WithTimeout(ctx, timeout)

	done := make(chan struct{})
	task := &BackgroundTask{
		ID:          taskID,
		Command:     command,
		Description: description,
		StartTime:   time.Now(),
		Status:      "running",
		Done:        done,
	}

	m.mu.Lock()
	m.tasks[taskID] = task
	m.mu.Unlock()

	// 设置取消函数（内部使用）
	go func() {
		defer cancel() // 确保调用 cancel 释放资源

		<-taskCtx.Done()
		// 任务完成/取消后清理
		m.mu.Lock()
		if t, ok := m.tasks[taskID]; ok {
			if t.Status == "running" {
				t.Status = "completed"
			}
		}
		m.mu.Unlock()
		close(done)
	}()

	return taskID, nil
}

// CompleteTask 完成任务
func (m *BackgroundTaskManager) CompleteTask(taskID string, result ToolResult, err error) {
	m.mu.Lock()
	task, ok := m.tasks[taskID]
	if ok {
		if err != nil {
			task.Status = "failed"
		} else {
			task.Status = "completed"
		}
		delete(m.tasks, taskID)
	}
	m.mu.Unlock()

	if !ok {
		return
	}

	if task.OnComplete != nil {
		task.OnComplete(result, err)
	}
}

// CancelTask 取消任务
func (m *BackgroundTaskManager) CancelTask(taskID string) error {
	m.mu.Lock()
	task, ok := m.tasks[taskID]
	m.mu.Unlock()

	if !ok {
		return ErrTaskNotFound
	}

	task.Status = "cancelled"
	// 注意：需要外部调用者调用 context.CancelFunc
	return nil
}

// GetTask 获取任务状态
func (m *BackgroundTaskManager) GetTask(taskID string) (*BackgroundTask, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	task, ok := m.tasks[taskID]
	return task, ok
}

// ListTasks 列出所有任务
func (m *BackgroundTaskManager) ListTasks() []*BackgroundTask {
	m.mu.RLock()
	defer m.mu.RUnlock()

	tasks := make([]*BackgroundTask, 0, len(m.tasks))
	for _, t := range m.tasks {
		tasks = append(tasks, t)
	}
	return tasks
}

// generateTaskID 生成任务 ID
func generateTaskID(prefix string) string {
	// 简单实现，可以用 uuid 替换
	return prefix + "_" + randomString(8)
}

// randomString 生成随机字符串
func randomString(n int) string {
	const letters = "abcdefghijklmnopqrstuvwxyz0123456789"
	b := make([]byte, n)
	for i := range b {
		idx := make([]byte, 1)
		rand.Read(idx)
		b[i] = letters[int(idx[0])%len(letters)]
	}
	return string(b)
}
