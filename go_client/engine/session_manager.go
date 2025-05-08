package engine

import (
	"context"
	"fmt"
	"github.com/google/uuid"
	"github.com/patrickmn/go-cache"
	"go.uber.org/zap"
	"go_client/config"
	"go_client/pkg/map_utils"
	"runtime/debug"
	"sort"
	"sync"
	"time"
)

func GenPushURL(preURL, streamKey string) string {
	return preURL + "/" + streamKey
}

type SessionManager struct {
	pushUrlInternalPre  string // 推流使用前缀 ：如 rtmp://rtmp-server/live/stream
	pushUrlPublicPre    string // 播放展示用：如 rtmp://mydomain.com/live/stream
	pushUrlPublicHlsPre string // 播放Hls展示用：如 http://mydomain.com/live/stream
	ctx                 context.Context
	cancel              context.CancelFunc
	logger              *zap.Logger
	cfg                 *config.Config
	sessionPool         sync.Pool
	sessions            *map_utils.Map[string, *Session]
	closeCh             chan string
	healthyHeartbeat    int32
	detectStore         DetectStore
	pullerRestart       PullStreamEOFRestart
}

func NewSessionManager(ctx context.Context, canalFunc context.CancelFunc, logger *zap.Logger, cfg *config.Config, store DetectStore, pullerRestart PullStreamEOFRestart) *SessionManager {
	return &SessionManager{
		pushUrlInternalPre:  cfg.Engine.PushUrlInternalPre,
		pushUrlPublicPre:    cfg.Engine.PushUrlPublicPre,
		pushUrlPublicHlsPre: cfg.Engine.PushUrlPublicHlsPre,
		ctx:                 ctx,
		cancel:              canalFunc,
		logger:              logger,
		sessionPool: sync.Pool{
			New: func() interface{} {
				s := new(Session)
				s.localCache = cache.New(5*time.Minute, 10*time.Minute)
				return s
			}},
		sessions: map_utils.New[string, *Session](),
		closeCh:  make(chan string, 128),
		//rwLock:   new(sync.RWMutex),
		cfg:              cfg,
		healthyHeartbeat: cfg.Engine.HealthyHeartbeat,
		detectStore:      store,
		pullerRestart:    pullerRestart,
	}
}

func (s *SessionManager) Run() {
	go s.closeChRecv()
	go s.checkHealthySession()
}

func (s *SessionManager) Close() {
	s.logger.Info("sessionManager close...")
	s.sessions.Range(func(key string, _session *Session) bool {
		if cancel := _session.GetCancelFunc(); cancel != nil {
			cancel()
		}
		s.sessions.Delete(key)
		_session.Reset()
		s.sessionPool.Put(_session)
		return true
	})
	s.cancel()
}

// closeChRecv 接收关闭session并处理
func (s *SessionManager) closeChRecv() {
	s.logger.Info("session manager closeChRecv running...")
	for {
		select {
		case <-s.ctx.Done():
			s.logger.Info("🛑 SessionManager 关闭session处理程序关闭")
			return
		case id := <-s.closeCh:
			_session, exists := s.sessions.Load(id)
			if !exists {
				continue
			}
			s.logger.Info(fmt.Sprintf(`📴 Stream session "%v" 关闭会话并清除`, id))
			if cancel := _session.GetCancelFunc(); cancel != nil {
				cancel()
			}
			s.sessions.Delete(id)

			// 重置_session 并放回池中
			_session.Reset()
			s.sessionPool.Put(_session)
		}

	}
}

// CheckHealthySession 检查会话健康
func (s *SessionManager) checkHealthySession() {
	s.logger.Info("session manager checkHealthySession running...")
	tick := time.Tick(time.Second * time.Duration(s.healthyHeartbeat))
	for {
		select {
		case <-s.ctx.Done():
			s.logger.Info("🛑 SessionManager 会话健康检查关闭")
			return
		case <-tick:
			s.sessions.Range(func(key string, _session *Session) bool {
				if !_session.runningStatus.Load() || (!_session.recordStatus.Load() && !_session.detectStatus.Load()) { // 非运行状态或 已无识别/记录状态
					// 避免重复 cancel 和清理
					if _session.handledClose.CompareAndSwap(false, true) {
						s.logger.Info("🧹 Tick 清理非运行 Session", zap.String("id", _session.id))

						if cancel := _session.GetCancelFunc(); cancel != nil {
							cancel()
						}
						s.sessions.Delete(key)
						s.sessionPool.Put(_session)

					}
				}
				return true
			})
		}
	}
}

func (s *SessionManager) CreateSession(id, rtsp, aiURL string, options ...SetSessionOption) (*Session, error) {
	if _, exists := s.sessions.Load(id); exists {
		return nil, fmt.Errorf("session already started: %s", id)
	}

	ctx, cancel := context.WithCancel(s.ctx)

	session := s.sessionPool.Get().(*Session)
	session.id = id
	session.rtspURL = rtsp
	session.cancelFunc = cancel
	session.ctx = ctx
	session.logger = s.logger
	session.closeCh = s.closeCh

	session.streamKey = uuid.New().String()

	session.pushMu = sync.Mutex{}
	session.pullMu = sync.Mutex{}
	session.recordMu = sync.Mutex{}

	session.SetSessionWithOptions(options...)
	session.resultCache = &DetectionResultCache{
		RWMutex: sync.RWMutex{},
		Results: make([]DetectionResult, 0),
	}
	session.frameForDetection = make(chan []byte, 8)
	session.rtspUpdateChRun = make(chan string, 1)
	session.rtspUpdateChRecording = make(chan string, 1)

	s.sessions.Store(id, session)

	if err := session.PreparePuller(); err != nil { // 创建拉流资源
		s.sessions.Delete(id)
		return nil, fmt.Errorf("failed to prepare stream: %w", err)
	}

	s.logger.Info("🚀 Session started", zap.String("id", id), zap.String("rtsp", rtsp))

	go func() {
		defer func() {
			if r := recover(); r != nil {
				s.logger.Error("panic recovered in Session.Run", zap.Any("error", r), zap.ByteString("stack", debug.Stack()))
			}
		}()
		session.Run(
			aiURL,
			s.cfg.Engine.UvicornSocket,
			s.cfg.Engine.SocketPath,
			s.cfg.Store.DetectResultPath+"/"+session.id,
			s.cfg.Store.DetectResultPathReal+"/"+session.id,
			s.detectStore,
			s.pullerRestart,
		)
	}()

	return session, nil
}

func (s *SessionManager) GetSessionDescList() []SessionDesc {
	descList := make([]SessionDesc, 0)
	s.sessions.Range(func(key string, _session *Session) bool {
		if !_session.runningStatus.Load() {
			return true
		}
		descList = append(descList, _session.GetDesc(s.pushUrlPublicPre, s.pushUrlPublicHlsPre))
		return true
	})

	sort.Slice(descList, func(i, j int) bool {
		return descList[i].ID < descList[j].ID
	})

	return descList
}

func (s *SessionManager) StopSessionRun(id string) error {
	if session, exists := s.sessions.Load(id); exists {
		session.cancelFunc()
		return nil
	}
	return fmt.Errorf("Session 不存在: %s", id)
}

func (s *SessionManager) StopSessionDetect(id string) error {
	if session, exists := s.sessions.Load(id); exists {
		session.detectStatus.Store(false)
		return nil
	}
	return nil
}

func (s *SessionManager) StartSessionDetect(id string, detectEndTimestamp int64) error {
	if session, exists := s.sessions.Load(id); exists {
		success := session.detectStatus.CompareAndSwap(false, true)
		if !success {
			// 已运行中，直接重置结束时间，返回
			session.detectEndTimestamp.Store(detectEndTimestamp)
			return nil
		}
		pushURL := GenPushURL(s.pushUrlInternalPre, session.streamKey)
		err := session.PreParePusher(pushURL) // 准备推流资源
		if err != nil {
			return err
		}
		s.logger.Info("pusher starting:", zap.String("id", id), zap.String("pushRTMPURL", pushURL))
		session.detectEndTimestamp.Store(detectEndTimestamp)
		return nil
	}
	return nil
}

func (s *SessionManager) StopSessionRecord(id string) error {
	if session, exists := s.sessions.Load(id); exists {
		session.recordStatus.Store(false)
		session.StopRecording()
		return nil
	}
	return nil
}

func (s *SessionManager) StartSessionRecord(id string, recordEndTimestamp int64, segment time.Duration) error {
	if session, exists := s.sessions.Load(id); exists {
		success := session.recordStatus.CompareAndSwap(false, true)
		if !success {
			// 已运行中，直接重置结束时间，返回
			session.recordEndTimestamp.Store(recordEndTimestamp)
			return nil
		}
		session.recordEndTimestamp.Store(recordEndTimestamp)
		recordPath, realPath := s.cfg.Store.RecordPath+"/"+session.id, s.cfg.Store.RecordPathReal+"/"+session.id
		err := session.StartRecording(recordPath, realPath, segment, s.detectStore, s.pullerRestart)
		if err != nil {
			return err
		}
		return nil
	}
	return nil
}

func (s *SessionManager) RemoveSession(id string) {
	_session, exists := s.sessions.Load(id)
	if !exists {
		return
	}
	_session.runningStatus.Store(false)
	s.sessions.Delete(id)
	if cancel := _session.GetCancelFunc(); cancel != nil {
		cancel()
	}
	_session.Reset()
	s.sessionPool.Put(_session)
}

func (s *SessionManager) GetSessionDescByID(id string) (SessionDesc, bool) {
	_session, exists := s.sessions.Load(id)
	if !exists {
		return SessionDesc{}, false
	}

	return _session.GetDesc(s.pushUrlPublicPre, s.pushUrlPublicHlsPre), true
}
