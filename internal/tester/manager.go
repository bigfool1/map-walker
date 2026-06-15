package tester

import (
	"context"
	"encoding/json"
	"log"
	"net/http"
	"os"
	"os/signal"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/coder/websocket"

	"map-walker/internal/auth"
	"map-walker/internal/realtime"
	"map-walker/internal/storage"
	"map-walker/internal/synthetic"
)

type Config struct {
	TargetCount int
	RampRate    int
	Host        string
	Port        string
	Password    string
}

type Manager struct {
	cfg Config
	db  *storage.DB

	clients   []*Client
	behaviors []*synthetic.Behavior
	userIDs   []int64
	sessions  []string // 原始 session token，用于 cookie

	activeCount atomic.Int32
	stop        chan struct{}
	done        chan struct{}
}

func NewManager(cfg Config, db *storage.DB) *Manager {
	return &Manager{
		cfg:  cfg,
		db:   db,
		stop: make(chan struct{}),
		done: make(chan struct{}),
	}
}

func (m *Manager) provision(ctx context.Context) {
	p := synthetic.NewProvisioner(m.db)
	result, err := p.Provision(ctx, m.cfg.TargetCount, 0, m.cfg.Password)
	if err != nil {
		log.Printf("provision 失败: %v", err)
		return
	}
	log.Printf("provision 完成: created=%d reused=%d corrected=%d failed=%d",
		result.Created, result.Reused, result.Corrected, result.Failed)
}

func (m *Manager) generateSessions(ctx context.Context) {
	users, err := m.db.LoadSyntheticUsers()
	if err != nil {
		log.Printf("加载合成用户失败: %v", err)
		return
	}

	byUsername := make(map[string]storage.SyntheticUserRecord, len(users))
	for _, u := range users {
		byUsername[u.Username] = u
	}

	m.userIDs = make([]int64, m.cfg.TargetCount)
	m.sessions = make([]string, m.cfg.TargetCount)

	for n := 1; n <= m.cfg.TargetCount; n++ {
		username := synthetic.FormatUsername(n)
		u, ok := byUsername[username]
		if !ok {
			log.Printf("账户 %s 未找到", username)
			continue
		}

		token, err := auth.NewSessionToken()
		if err != nil {
			log.Printf("生成 session token 失败 %s: %v", username, err)
			continue
		}

		now := time.Now()
		session := storage.Session{
			TokenHash: auth.HashSessionToken(token),
			UserID:    u.UserID,
			CreatedAt: now,
			ExpiresAt: now.Add(auth.SessionDuration),
		}
		if err := m.db.CreateSession(session); err != nil {
			log.Printf("创建 session 失败 %s: %v", username, err)
			continue
		}

		m.userIDs[n-1] = u.UserID
		m.sessions[n-1] = token
	}
}

func (m *Manager) rampUp(ctx context.Context) {
	m.clients = make([]*Client, m.cfg.TargetCount)
	m.behaviors = make([]*synthetic.Behavior, m.cfg.TargetCount)

	placementCfg := synthetic.DefaultPlacementConfig()
	behaviorCfg := synthetic.DefaultBehaviorConfig()

	for n := 1; n <= m.cfg.TargetCount; n++ {
		if m.cfg.RampRate > 0 {
			time.Sleep(time.Second / time.Duration(m.cfg.RampRate))
		}
		m.connectOne(ctx, n, placementCfg, behaviorCfg)
	}
}

func (m *Manager) connectOne(ctx context.Context, accountNumber int, placementCfg synthetic.PlacementConfig, behaviorCfg synthetic.BehaviorConfig) {
	idx := accountNumber - 1
	token := m.sessions[idx]
	if token == "" {
		log.Printf("账户 %d 无 session token，跳过", accountNumber)
		return
	}

	cookie := &http.Cookie{Name: auth.CookieName, Value: token}
	header := http.Header{}
	header.Add("Cookie", cookie.String())

	url := "ws://" + m.cfg.Host + ":" + m.cfg.Port + "/ws"
	conn, _, err := websocket.Dial(ctx, url, &websocket.DialOptions{
		HTTPHeader: header,
	})
	if err != nil {
		log.Printf("连接 %s (账户 %d) 失败: %v", url, accountNumber, err)
		return
	}

	client := NewClient(conn)
	go client.Run(context.Background())

	lat, lng := synthetic.PlacementLatLng(placementCfg, accountNumber)
	// NewBehavior 参数顺序：(accountNumber, cfg, lat, lng)
	behavior := synthetic.NewBehavior(accountNumber, behaviorCfg, lat, lng)

	m.clients[idx] = client
	m.behaviors[idx] = behavior
	m.activeCount.Add(1)
}

func (m *Manager) tickLoop(ctx context.Context) {
	const tickInterval = 100 * time.Millisecond
	ticker := time.NewTicker(tickInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-m.stop:
			return
		case <-ticker.C:
			m.doTick(tickInterval)
		}
	}
}

func (m *Manager) doTick(tickInterval time.Duration) {
	n := len(m.behaviors)
	if n == 0 {
		return
	}
	stagger := tickInterval / time.Duration(n)

	for i, b := range m.behaviors {
		if b == nil || m.clients[i] == nil {
			time.Sleep(stagger)
			continue
		}
		input, changed := b.OnTick()
		if changed {
			msg := realtime.InputMessage{
				Type:     realtime.MessageTypeInput,
				Sequence: input.Sequence,
				Up:       input.Up,
				Down:     input.Down,
				Left:     input.Left,
				Right:    input.Right,
			}
			data, err := json.Marshal(msg)
			if err == nil {
				m.clients[i].Send(data)
			}
		}
		time.Sleep(stagger)
	}
}

func (m *Manager) statsLoop(ctx context.Context) {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-m.stop:
			return
		case <-ticker.C:
			m.dumpStats()
		}
	}
}

func (m *Manager) dumpStats() {
	active := m.activeCount.Load()
	var writeFails uint64
	for _, c := range m.clients {
		if c != nil {
			writeFails += c.WriteFails()
		}
	}
	log.Printf("active=%d writeFails=%d", active, writeFails)
}

func (m *Manager) Run() {
	ctx := context.Background()

	// 1. 预创建账户
	if m.cfg.Password != "" {
		m.provision(ctx)
	}

	// 2. 生成会话
	m.generateSessions(ctx)

	// 3. 启动连接（内部初始化 clients/behaviors 数组）
	m.rampUp(ctx)

	// 4. 启动 tick 和 stats 循环
	go m.tickLoop(ctx)
	go m.statsLoop(ctx)

	// 5. 等待退出信号
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	<-sigCh
	log.Println("收到信号，开始关闭")

	close(m.stop)
	m.shutdown()
	close(m.done)
}

func (m *Manager) shutdown() {
	for _, c := range m.clients {
		if c != nil {
			c.Close()
		}
	}
	for _, c := range m.clients {
		if c != nil {
			c.WaitDone()
		}
	}
}
