package engine

import (
	"context"
	"errors"
	"fmt"
	"github.com/google/uuid"
	"image"
	"image/color"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime/debug"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/patrickmn/go-cache"
	"go.uber.org/zap"
	"gocv.io/x/gocv"
)

type SessionDesc struct {
	ID                 string `json:"id"`                 // 唯一标识
	StreamKey          string `json:"streamKey"`          // 用于拼接 RTMP 推流地址
	PushUrlPublic      string `json:"pushUrlPublic"`      // 播放展示用
	PushHlsURL         string `json:"pushHlsUrl"`         // 播放展示HlsURL
	DetectStatus       bool   `json:"detectStatus"`       // 识别状态 false 停止 true 识别
	RecordStatus       bool   `json:"recordStatus"`       // 录制状态 false 停止 true 录制中
	RunningStatus      bool   `json:"runningStatus"`      // 运行状态 false 关闭 true 运行中
	DetectEndTimestamp int64  `json:"detectEndTimestamp"` // 识别结束时间戳
	RecordEndTimestamp int64  `json:"recordEndTimestamp"` // 录制结束时间戳
}

type DetectionResultCache struct {
	sync.RWMutex
	Results []DetectionResult
}

/*
	TODO 拉流 - 识别 - 推流 分别启用不同的 goroutine 并使用 sync.One 来实现
	拉流： 拉流创建- 拉流结束（直接canal）
	推流： 开启推流 暂停推流
*/

// 对象池定义
var (
	imgBufPool = sync.Pool{
		New: func() interface{} {
			return make([]byte, 1920*1080*3) // 默认最大分辨率
		},
	}
	matPool = sync.Pool{
		New: func() interface{} {
			mat := gocv.NewMat()
			return &mat // 返回指针
		},
	}
)

// FFmpeg进程管理相关的常量
const (
	defaultFFmpegBufferSize = 1024 * 1024 // 1MB buffer
	maxFFmpegRetries        = 3
	ffmpegRestartDelay      = time.Second * 2
)

// Session 流会话
type Session struct {
	width      int //  宽
	height     int //  高
	framerate  int // 帧率
	retryTimes int // 拉流发生EOF最大重试次数

	id        string // 唯一标识
	streamKey string // 用于拼接 RTMP 推流地址
	rtspURL   string // 拉流链接

	detectEndTimestamp atomic.Int64 // 识别结束时间戳
	recordEndTimestamp atomic.Int64 // 录制结束时间戳

	lastResetAt atomic.Int64

	detectStatus       atomic.Bool // 识别状态 false 停止 true 识别中
	recordStatus       atomic.Bool // 录制状态 false 停止 true 录制中
	runningStatus      atomic.Bool // 运行状态 false 关闭 true 运行中
	pullEOFAutoRestart atomic.Bool // 拉流发生EOF 是否重新拉流

	handledClose atomic.Bool
	ctx          context.Context
	cancelFunc   context.CancelFunc
	logger       *zap.Logger

	closeCh chan<- string

	pullMu        sync.Mutex // 拉流锁
	pullFFmpegCmd *exec.Cmd  // FFmpeg 拉流Cmd
	pullReader    io.Reader  // 拉流Reader

	pushMu        sync.Mutex     // 推流锁
	pushStdin     io.WriteCloser //  FFmpeg 推流进程的 stdin 管道
	pushFFmpegCmd *exec.Cmd      // FFmpeg 推流Cmd

	recordMu        sync.Mutex     // 录制锁
	recordFFmpegCmd *exec.Cmd      // 录制FFmpegCmd
	recordStdin     io.WriteCloser // 录制 stdin 管道

	resultCache       *DetectionResultCache
	frameForDetection chan []byte

	localCache *cache.Cache // 内部缓存

	imgBuf []byte
	img    *gocv.Mat // 保持为指针类型

	ffmpegBuffer []byte

	rtspUpdateChRun       chan string
	rtspUpdateChRecording chan string
}

type SetSessionOption func(s *Session)

func SetSessionWith(with int) SetSessionOption {
	return func(s *Session) {
		s.width = with
	}
}

func SetSessionHeight(height int) SetSessionOption {
	return func(s *Session) {
		s.height = height
	}
}

func SetSessionFramerate(framerate int) SetSessionOption {
	return func(s *Session) {
		s.framerate = framerate
	}
}

func SetSessionVideoStreamConfig(with, height, framerate int) SetSessionOption {
	return func(s *Session) {
		s.width = with
		s.height = height
		s.framerate = framerate
	}
}

func SetPullerEOFAutoRestart(retryTimes int, autoRestart bool) SetSessionOption {
	return func(s *Session) {
		s.retryTimes = retryTimes
		s.pullEOFAutoRestart.Store(autoRestart)
	}
}

func NewSessionWithCtx(id, rtsp string, ctx context.Context, cancelFunc context.CancelFunc, logger *zap.Logger, closeCh chan<- string, options ...SetSessionOption) *Session {
	s := &Session{
		detectStatus:          atomic.Bool{},
		runningStatus:         atomic.Bool{},
		ctx:                   ctx,
		cancelFunc:            cancelFunc,
		id:                    id,
		closeCh:               closeCh,
		rtspURL:               rtsp,
		logger:                logger,
		localCache:            cache.New(1*time.Minute, 2*time.Minute),
		imgBuf:                imgBufPool.Get().([]byte),
		img:                   matPool.Get().(*gocv.Mat), // 从对象池获取指针
		ffmpegBuffer:          make([]byte, defaultFFmpegBufferSize),
		rtspUpdateChRun:       make(chan string, 1),
		rtspUpdateChRecording: make(chan string, 1),
	}

	// set option
	for i := range options {
		if options[i] != nil {
			options[i](s)
		}
	}

	return s
}

func (s *Session) SetSessionWithOptions(options ...SetSessionOption) {
	// set option
	for i := range options {
		if options[i] != nil {
			options[i](s)
		}
	}
}

func (s *Session) GetCancelFunc() context.CancelFunc {
	return s.cancelFunc
}

func (s *Session) Reset() {
	// 停止上下文
	if s.cancelFunc != nil {
		s.cancelFunc()
	}

	// 重置状态
	s.detectStatus.Store(false)
	s.runningStatus.Store(false)
	s.handledClose.Store(false)
	s.recordStatus.Store(false)

	// 停止拉流 FFmpeg 进程
	//if s.pullFFmpegCmd != nil && s.pullFFmpegCmd.Process != nil {
	//	_ = s.pullFFmpegCmd.Process.Kill()
	//	_ = s.pullFFmpegCmd.Wait()
	//}
	//s.pullFFmpegCmd = nil
	//s.pullReader = nil
	s.ClearPuller()

	// 停止推流 FFmpeg 进程
	//if s.pushFFmpegCmd != nil && s.pushFFmpegCmd.Process != nil {
	//	_ = s.pushFFmpegCmd.Process.Kill()
	//	_ = s.pushFFmpegCmd.Wait()
	//}
	//s.pushFFmpegCmd = nil
	//
	//// 关闭 FFmpeg stdin 写入管道
	//if s.pushStdin != nil {
	//	_ = s.pushStdin.Close()
	//}
	//s.pushStdin = nil
	s.ClearPusher()

	//if s.recordFFmpegCmd != nil && s.recordFFmpegCmd.Process != nil {
	//	_ = s.recordFFmpegCmd.Process.Kill()
	//	_ = s.recordFFmpegCmd.Wait()
	//}
	//s.recordFFmpegCmd = nil
	s.ClearRecorder()

	// 清空上下文和控制函数
	s.ctx = nil
	s.cancelFunc = nil

	// 清空基本信息
	s.id = ""
	s.streamKey = ""
	s.rtspURL = ""

	s.resultCache = &DetectionResultCache{}

	s.localCache.Flush() // 只删除缓存不置空 防止频繁创建对象

	// 归还对象到对象池
	if s.imgBuf != nil {
		imgBufPool.Put(s.imgBuf)
		s.imgBuf = nil
	}
	if s.img != nil {
		s.img.Close()
		matPool.Put(s.img)
		s.img = nil
	}

	// 不清空 logger 和 closeCh —— 这些是注入的全局组件，不应被置 nil

}

// SendIDToCloseCh 发送会话ID到关闭处理通道
func (s *Session) SendIDToCloseCh() {
	if !s.handledClose.CompareAndSwap(false, true) {
		return // 已处理
	}
	defer func() {
		if r := recover(); r != nil && s.logger != nil {
			s.logger.Warn("SendCloseCh recovered from panic", zap.Any("error", r))
		}
	}()
	if s.closeCh != nil {
		s.closeCh <- s.id // 若使用无缓冲通道，需注意阻塞
	}
}

func (s *Session) GetCanalFunc() context.CancelFunc {
	return s.cancelFunc
}

func (s *Session) GetCtx() context.Context {
	return s.ctx
}

func (s *Session) GetDesc(pushUrlPublicPre, hlsPre string) SessionDesc {
	return SessionDesc{
		ID:                 s.id,
		StreamKey:          s.streamKey,
		PushUrlPublic:      pushUrlPublicPre + "/" + s.streamKey,
		PushHlsURL:         hlsPre + "/" + s.streamKey + ".m3u8",
		DetectStatus:       s.detectStatus.Load(),
		RecordStatus:       s.recordStatus.Load(),
		RunningStatus:      s.runningStatus.Load(),
		DetectEndTimestamp: s.detectEndTimestamp.Load(),
		RecordEndTimestamp: s.recordEndTimestamp.Load(),
	}
}

// PreparePuller 拉流前预备
func (s *Session) PreparePuller() (err error) {
	s.logger.Info(fmt.Sprintf("📽️ Session starting: url=%s, res=%dx%d, fps=%d", s.rtspURL, s.width, s.height, s.framerate))

	// 先清理一遍资源防止 遗留资源 cmd /stdin 进程
	s.ClearPuller() //  清理拉流资源

	defer func() {
		if err != nil {
			s.ClearPuller() // 仅在出错时回收
		}
	}()

	// 启动拉流 FFmpeg（RTSP → stdout）
	pullCmd, stdout, err := startFFmpegReader(s.rtspURL, s.width, s.height, s.framerate)
	if err != nil {
		return fmt.Errorf("FFmpeg 拉流失败: %w", err)
	}

	s.SetPuller(pullCmd, stdout)

	s.runningStatus.Store(true)
	return nil
}

func (s *Session) PreParePusher(pushRTMPURL string) (err error) {
	s.ClearPusher() // 先清理一遍防止遗留
	defer func() {
		if err != nil {
			s.ClearPusher() // 仅在出错时回收
		}
	}()

	// 启动推流 FFmpeg（stdin → RTMP）
	pushCmd, pushIO, err := startFFmpegPusher(s.width, s.height, float64(s.framerate), false, pushRTMPURL, s.logger)
	if err != nil {
		return fmt.Errorf("FFmpeg 推流失败: %w", err)
	}
	s.SetPusher(pushCmd, pushIO)

	s.logger.Info("推流 FFmpeg 初始化完成")
	return nil
}

func (s *Session) Run(aiDetectAIURL string, uvicornSocket bool, socketPath string, resultPath, resultPathReal string, detectStore DetectStore, pullRestart PullStreamEOFRestart) {
	defer func() {
		go func() {
			_ = detectStore.PushSessionEndNotify(context.Background(), s.id)
		}()
		if r := recover(); r != nil {
			s.logger.Error("❌ Panic recovered in Run", zap.Any("error", r), zap.Any("stacktrace", string(debug.Stack())))
		}

		// 关闭资源
		s.runningStatus.Store(false)
		s.ClearPuller()
		s.ClearPusher()
		s.ClearRecorder()
		s.recordEndTimestamp = atomic.Int64{}
		s.detectStatus.Store(false)
		s.recordStatus.Store(false)
		s.SendIDToCloseCh()
		s.Reset()
		s.logger.Info("📴 Stream session stopped")
	}()

	if err := os.MkdirAll(resultPath, 0755); err != nil {
		s.logger.Error("创建识别结果目录失败", zap.Error(err))
		return
	}

	// 初始化必要的组件
	if s.frameForDetection == nil {
		s.frameForDetection = make(chan []byte, 8) // 添加缓冲区大小
	}

	s.resultCache = &DetectionResultCache{}

	// 检查并初始化图像缓冲区
	if s.imgBuf == nil {
		s.imgBuf = imgBufPool.Get().([]byte)
	}

	// 检查并初始化Mat对象
	if s.img == nil {
		s.img = matPool.Get().(*gocv.Mat)
	}

	// 使用对象池中的缓冲区
	if cap(s.imgBuf) < s.width*s.height*3 {
		// 如果缓冲区太小，重新分配
		imgBufPool.Put(s.imgBuf)
		s.imgBuf = make([]byte, s.width*s.height*3)
	}
	s.imgBuf = s.imgBuf[:s.width*s.height*3]

	// 确保所有必要的组件都已初始化
	if s.pullReader == nil {
		if err := s.PreparePuller(); err != nil {
			s.logger.Error("Failed to prepare puller", zap.Error(err))
			return
		}
	}

	// 异步识别 goroutine
	go s.asyncDetectLoop(uvicornSocket, socketPath, aiDetectAIURL, resultPath, resultPathReal, detectStore)

	lastDetect := time.Now()
	detectInterval := time.Second / 5 // 每秒识别 5 帧
	frameCount := 0
	skipFrames := 2 // 每隔2帧处理一次，减少CPU使用

	eofTimes := 0
	for {
		select {
		case <-s.ctx.Done():
			s.logger.Info("🛑 Context canceled")
			return
		case newRtsp := <-s.rtspUpdateChRun:
			s.logger.Info("🔁 Run 收到拉流地址更新", zap.String("newRtsp", newRtsp))
			if err := s.PreparePuller(); err != nil {
				continue
			}
			continue
		default:
			if !s.runningStatus.Load() {
				return
			}

			// 从拉流中读取数据
			err := s.PullerRead(s.imgBuf)
			if err != nil {
				if err == io.EOF || errors.Is(err, PullReaderIsNil) {
					eofTimes++
					if !s.pullEOFAutoRestart.Load() || eofTimes > s.retryTimes { // 无需重新拉流
						s.logger.Error("❌EOF 检测到 EOF，流断开，无需重新拉流,终止", zap.String("id", s.id))
						s.cancelFunc()
						return
					}
					s.logger.Error("❌EOF 检测到 EOF，流断开，终止", zap.String("id", s.id))

					rtspURL, err := pullRestart.ReGetRtspURL(s.id)
					if err != nil || rtspURL == "" {
						s.logger.Error("❌ 重新获取拉流地址失败", zap.String("id", s.id), zap.Error(err))
						// todo 是直接退出还是重试？？
						s.cancelFunc()
						return
					}
					s.logger.Info("🔁 成功获取新拉流地址", zap.String("id", s.id), zap.String("url", rtspURL))
					err = s.ResetRtspURL(rtspURL)
					if err != nil {
						s.cancelFunc()
						s.logger.Info("❌ 🔁 ResetRtspURL 错误", zap.String("id", s.id), zap.String("url", rtspURL))
						return
					}
					continue
				}

				// 非EOF错误继续
				s.logger.Info("❌ 读取帧错误，跳过当前帧", zap.String("id", s.id), zap.Error(err))
				continue
			}

			frameCount++
			if frameCount%skipFrames != 0 {
				// 跳过部分帧，减少处理负担
				continue
			}
			if !s.detectStatus.Load() || time.Now().Unix() >= s.detectEndTimestamp.Load() {
				s.detectStatus.CompareAndSwap(true, false)
				//s.ClearPusher() // 停止推流进程
				continue // 无需目标检测或推流，继续下一帧
			}

			// 将帧数据转换为 gocv.Mat
			imgTmp, err := gocv.NewMatFromBytes(s.height, s.width, gocv.MatTypeCV8UC3, s.imgBuf)
			if err == nil && !imgTmp.Empty() {
				if s.img != nil {
					s.img.Close()
				}
				s.img = matPool.Get().(*gocv.Mat)
				*s.img = imgTmp.Clone()
				imgTmp.Close()
			} else {
				continue
			}

			// 控制识别频率（基于时间）
			if s.detectStatus.Load() && time.Since(lastDetect) >= detectInterval {
				lastDetect = time.Now()

				func() {
					s.resultCache.Lock()
					defer s.resultCache.Unlock()
					s.resultCache.Results = nil
				}()

				if s.img != nil && !s.img.Empty() { // ✅ 避免 nil dereference
					// 编码并入识别队列
					imgBytes, err := gocv.IMEncodeWithParams(gocv.JPEGFileExt, *s.img, []int{
						gocv.IMWriteJpegQuality, 85,
					})
					if err != nil {
						s.logger.Error("图像编码失败", zap.Error(err))
					} else {
						select {
						case s.frameForDetection <- imgBytes.GetBytes():
						default:
							s.logger.Debug("识别队列已满，跳过当前帧")
						}
					}

				} else {
					s.logger.Warn("跳过当前帧：s.img 为 nil 或为空")
				}
			}

			// 获取最新的检测结果
			var latestResults []DetectionResult
			func() {
				s.resultCache.RLock()
				defer s.resultCache.RUnlock()
				if len(s.resultCache.Results) > 0 {
					latestResults = make([]DetectionResult, len(s.resultCache.Results))
					copy(latestResults, s.resultCache.Results)
				}
			}()

			// 应用副本的识别结果
			if len(latestResults) > 0 && s.img != nil {
				for _, r := range latestResults {
					rect := image.Rect(r.X1, r.Y1, r.X2, r.Y2)
					_ = gocv.Rectangle(s.img, rect, color.RGBA{0, 255, 0, 0}, 2)
					_ = gocv.PutText(s.img, r.Label, image.Pt(r.X1, r.Y1-10),
						gocv.FontHersheyPlain, 1.2, color.RGBA{255, 0, 0, 0}, 2)
				}
			}

			if s.detectStatus.Load() && s.img != nil && !s.img.Empty() {
				// 推流
				err = s.PusherWrite(s.img.ToBytes())
				if err != nil {
					s.logger.Error(fmt.Sprintf("[-] sessionID:%s 写入推流失败", s.id), zap.Error(err))
					s.cancelFunc()
					return
				}
			}

		}
	}
}

func isRetryableError(err error) bool {
	if err == io.ErrUnexpectedEOF {
		return true
	}
	if err == io.EOF {
		return false // EOF 是流终止
	}

	// 判断是否是超时
	if netErr, ok := err.(net.Error); ok && netErr.Timeout() {
		return true
	}
	// 一些 FFmpeg 的错误会包含具体字符串（不标准，但实用）
	if strings.Contains(err.Error(), "resource temporarily unavailable") {
		return true
	}
	return false
}

func (s *Session) asyncDetectLoop(uvicornSocket bool, socketPath string, aiDetectAIURL string, resultPath, resultPathReal string, detectStore DetectStore) {
	for {
		select {
		case <-s.ctx.Done():
			return
		case frameBytes := <-s.frameForDetection:
			if frameBytes == nil {
				continue
			}
			var results []DetectionResult
			var err error
			if uvicornSocket {
				results, err = detectObjectsUvicronSocket(frameBytes, socketPath, aiDetectAIURL)
			} else {
				results, err = detectObjects(frameBytes, aiDetectAIURL)
			}

			if err != nil {
				s.logger.Error("识别失败", zap.Error(err))
				continue
			}

			if len(results) > 0 {
				go func() {
					// todo 存储结果
					var store = false
					var labels []string
					for i := range results {
						if results[i].Label == "" {
							continue
						}
						_, exist := s.localCache.Get(results[i].Label)
						if !exist {
							store = true
							err = s.localCache.Add(results[i].Label, struct{}{}, time.Minute*5)
							if err != nil {
								s.logger.Error("session localCache 保存失败", zap.Error(err))
							}
						}
					}
					if !store {
						return // 没有新标签，不保存图片也不推送
					}

					// 将 JPEG 解码为 gocv.Mat
					img, err := gocv.IMDecode(frameBytes, gocv.IMReadColor)
					if err != nil {
						s.logger.Error("图像解码失败", zap.Error(err))
						return
					}
					defer img.Close()
					// 标注图像
					for _, r := range results {
						rect := image.Rect(r.X1, r.Y1, r.X2, r.Y2)
						_ = gocv.Rectangle(&img, rect, color.RGBA{0, 255, 0, 0}, 2)
						_ = gocv.PutText(&img, r.Label, image.Pt(r.X1, r.Y1-10),
							gocv.FontHersheyPlain, 1.2, color.RGBA{255, 0, 0, 0}, 2)
					}

					// 保存图像
					filename := fmt.Sprintf("detected_%d.jpg", time.Now().UnixNano())
					//fullPath := filepath.Join("detected_images", filename)
					fullPath := filepath.Join(resultPath, filename)
					fullPathReal := filepath.Join(resultPathReal, filename)
					if ok := gocv.IMWrite(fullPath, img); !ok {
						s.logger.Error("图像保存失败", zap.String("path", fullPath))
						return
					}

					_ctx, _cancelFunc := context.WithTimeout(context.Background(), time.Second*10)
					defer _cancelFunc()
					data := StoreDetectResultDto{
						Labels:    labels,
						PathURL:   fullPathReal,
						Timestamp: time.Now().Unix(),
						ID:        s.id,
					}
					err = detectStore.StoreDetectResultImage(_ctx, data)
					if err != nil {
						s.logger.Error("图像识别结果推送失败", zap.String("path", fullPathReal))
						return
					}
				}()

			}

			func() {
				s.resultCache.Lock()
				defer s.resultCache.Unlock()
				s.resultCache.Results = results
			}()

		}
	}
}

func (s *Session) StartRecording(recordDir, realDir string, segment time.Duration, detectStore DetectStore, pullRestart PullStreamEOFRestart) error {
	s.logger.Info("▶️ 开始录制（RTSP直录）", zap.String("dir", recordDir), zap.Duration("segment", segment))

	if err := os.MkdirAll(recordDir, 0755); err != nil {
		s.logger.Error("创建录制目录失败", zap.Error(err))
		return err
	}

	s.recordStatus.Store(true)

	go func() {
		defer func() {
			s.recordStatus.Store(false)
			s.recordEndTimestamp.Store(0)
			s.ClearRecorder()
		}()
		eofTimes := 0

		for {
			select {
			case <-s.ctx.Done():
				s.logger.Info("⏹️ 录制任务被取消", zap.String("dir", recordDir))
				return

			case newRtsp := <-s.rtspUpdateChRecording:
				s.logger.Info("🔁 收到拉流地址更新（录制）", zap.String("newRtsp", newRtsp))
				s.ClearRecorder()
				// 不 return，继续循环，下一段会使用新地址

			default:
				now := time.Now()
				if !s.recordStatus.Load() || now.Unix() >= s.recordEndTimestamp.Load() {
					s.logger.Info("⏹️ 录制任务结束")
					return
				}

				formatTime := uuid.New().String()
				segmentPath := filepath.Join(recordDir, formatTime+".mp4")
				segmentPathReal := filepath.Join(realDir, formatTime+".mp4")
				s.logger.Info("📽️ 开始录制分段", zap.String("file", segmentPath))

				cmd := exec.CommandContext(s.ctx, "ffmpeg",
					"-loglevel", "quiet",
					"-rtsp_transport", "tcp",
					"-timeout", "5000000",
					"-analyzeduration", "5000000",
					"-probesize", "10000000",
					"-i", s.rtspURL,
					"-c:v", "libx264",
					"-preset", "ultrafast",
					"-tune", "zerolatency",
					"-pix_fmt", "yuv420p",
					"-movflags", "+faststart",
					"-f", "mp4",
					"-an",
					"-t", fmt.Sprintf("%.0f", segment.Seconds()),
					"-y", segmentPath,
				)
				cmd.Stderr = os.Stderr

				start := time.Now().Unix()
				err := cmd.Run()
				end := time.Now().Unix()

				if err != nil {
					s.logger.Error("❌ FFmpeg 录制失败", zap.Error(err))

					// 尝试重拉 RTSP 地址（只要在运行状态）
					if s.runningStatus.Load() {
						eofTimes++
						if !s.pullEOFAutoRestart.Load() || eofTimes > s.retryTimes { // 无需重新拉流
							s.logger.Error("❌EOF 检测到 EOF，流断开，无需重新拉流,终止", zap.String("id", s.id))
							s.cancelFunc()
							return
						}

						if rtspURL, err := pullRestart.ReGetRtspURL(s.id); err == nil && rtspURL != "" {
							_ = s.ResetRtspURL(rtspURL)
							s.logger.Info("🔁 拉流地址已自动切换", zap.String("newRtsp", rtspURL))
							continue
						}
					}

					return // 不重连，退出
				}

				s.ClearRecorder()

				s.logger.Info("✅ 分段完成", zap.String("file", segmentPath))

				go func(data StoreRecordVideoDto) {
					ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
					defer cancel()
					if err := detectStore.StoreRecordVideo(ctx, data); err != nil {
						s.logger.Error("🔴 推送视频记录失败", zap.Error(err))
					} else {
						s.logger.Info("✅ 推送视频记录完成", zap.Any("video", data))
					}
				}(StoreRecordVideoDto{
					PathURL:        segmentPathReal,
					StartTimestamp: start,
					EndTimestamp:   end,
					ID:             s.id,
				})

				time.Sleep(500 * time.Millisecond)
			}
		}
	}()

	return nil
}

func (s *Session) StopRecording() {
	s.recordStatus.Store(false)
	s.ClearRecorder()
	s.logger.Info("📋 录制已手动停止")
}

func (s *Session) ResetRtspURL(rtspURL string) error {
	nowUnix := time.Now().Unix()
	last := s.lastResetAt.Load()
	if nowUnix-last < 5 {
		s.logger.Warn("⚠️ ResetRtspURL 触发过于频繁，已跳过", zap.String("id", s.id))
		return nil
	}
	s.lastResetAt.Store(nowUnix)

	s.ClearPuller()
	s.ClearRecorder()
	s.rtspURL = rtspURL
	err := s.PreparePuller()
	if err == nil {
		s.BroadcastRtspUpdate(nowUnix, rtspURL)
	}
	return err
}

// BroadcastRtspUpdate 向两个模块的通道广播拉流地址变更
func (s *Session) BroadcastRtspUpdate(nowUnix int64, newURL string) {
	if !s.runningStatus.Load() {
		// 已关闭不广播
		return
	}

	//if s.detectStatus.Load() && nowUnix <= s.detectEndTimestamp.Load() { // 只在 识别有效内推送

	// 由于 拉流一直存在所以需要一直推送更新
	select {
	case s.rtspUpdateChRun <- newURL:
	default:
	}
	//}

	if s.recordStatus.Load() && nowUnix <= s.recordEndTimestamp.Load() { // 只在 录制有效内推送
		select {
		case s.rtspUpdateChRecording <- newURL:
		default:
		}
	}

}
