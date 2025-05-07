package engine

import (
	"context"
	"errors"
	"fmt"
	"image"
	"image/color"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
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

// 修改对象池定义
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

// 添加FFmpeg进程管理相关的常量
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
		detectStatus:  atomic.Bool{},
		runningStatus: atomic.Bool{},
		ctx:           ctx,
		cancelFunc:    cancelFunc,
		id:            id,
		closeCh:       closeCh,
		rtspURL:       rtsp,
		logger:        logger,
		localCache:    cache.New(1*time.Minute, 2*time.Minute),
		imgBuf:        imgBufPool.Get().([]byte),
		img:           matPool.Get().(*gocv.Mat), // 从对象池获取指针
		ffmpegBuffer:  make([]byte, defaultFFmpegBufferSize),
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

	s.logger.Info("拉流与推流 FFmpeg 初始化完成")
	return nil
}

func (s *Session) Run(aiDetectAIURL string, uvicornSocket bool, socketPath string, resultPath, resultPathReal string, detectStore DetectStore, pullRestart PullStreamEOFRestart) {
	defer func() {
		if r := recover(); r != nil {
			s.logger.Error("❌ Panic recovered in Run", zap.Any("error", r))
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
		s.logger.Info("📴 Stream session stopped")
	}()

	// 初始化必要的组件
	if s.frameForDetection == nil {
		s.frameForDetection = make(chan []byte, 10) // 添加缓冲区大小
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
		default:
			if !s.runningStatus.Load() {
				return
			}

			err := s.PullerRead(s.imgBuf)
			if err != nil {
				if err == io.EOF || errors.Is(err, PullReaderIsNil) {
					eofTimes++
					s.logger.Error("❌EOF 检测到 EOF，流断开，终止", zap.String("id", s.id))
					if !s.pullEOFAutoRestart.Load() || eofTimes > s.retryTimes { // 无需重新拉流
						s.cancelFunc()
						return
					}

					rtspURL, err := pullRestart.ReGetRtspURL(s.id)
					if err != nil || rtspURL == "" {
						s.logger.Error("❌ 重新获取拉流地址失败", zap.String("id", s.id), zap.Error(err))
						s.cancelFunc()
						return
					}
					s.logger.Info("🔁 成功获取新拉流地址", zap.String("url", rtspURL))
					s.ClearPuller()
					s.rtspURL = rtspURL
					err = s.PreparePuller()
					if err != nil {
						s.logger.Error("❌ 重新拉流启动失败", zap.String("id", s.id), zap.Error(err))
						s.cancelFunc()
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

			if imgTmp, err := gocv.NewMatFromBytes(s.height, s.width, gocv.MatTypeCV8UC3, s.imgBuf); err == nil && !imgTmp.Empty() {
				if s.img != nil {
					s.img.Close()
				}
				s.img = &imgTmp // 使用指针
			} else {
				continue
			}

			// 写入录制
			if s.recordStatus.Load() && s.recordStdin != nil {
				err = s.RecorderWrite(s.imgBuf)
				if err != nil {
					s.logger.Error("写入录制失败", zap.Error(err))
				}
			}

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

			// 控制识别频率（基于时间）
			if s.detectStatus.Load() && time.Since(lastDetect) >= detectInterval && s.img != nil {
				lastDetect = time.Now()
				func() {
					s.resultCache.Lock()
					defer s.resultCache.Unlock()
					s.resultCache.Results = nil
				}()

				// 使用正确的IMEncodeWithParams参数
				imgBytes, err := gocv.IMEncodeWithParams(gocv.JPEGFileExt, *s.img, []int{
					gocv.IMWriteJpegQuality, 85,
				})
				if err != nil {
					s.logger.Error("图像编码失败", zap.Error(err))
					continue
				}

				select {
				case s.frameForDetection <- imgBytes.GetBytes():
				default:
					s.logger.Debug("识别队列已满，跳过当前帧")
				}

				// 只有 识别才开启实时流...
				// 推送给 FFmpeg 推流进程
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
							err = s.localCache.Add(results[i].Label, struct{}{}, time.Minute*30)
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
					fullPath := resultPath + filename
					fullPathReal := resultPathReal + filename
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

func (s *Session) StartRecording(recordDir, realDir string, segment time.Duration, detectStore DetectStore) error {
	s.logger.Info("▶️ 开始录制（RTSP直录）", zap.String("dir", recordDir), zap.Duration("segment", segment))

	// 确保目录存在
	if err := os.MkdirAll(recordDir, 0755); err != nil {
		s.logger.Error("创建录制目录失败", zap.Error(err))
		return err
	}

	s.recordStatus.Store(true)
	s.recordEndTimestamp.Store(time.Now().Add(segment).Unix())

	go func() {
		defer func() {
			s.recordStatus.Store(false)
			s.recordEndTimestamp.Store(0)
			s.ClearRecorder() // 清理录制资源
		}()

		for {
			if !s.recordStatus.Load() || time.Now().Unix() >= s.recordEndTimestamp.Load() {
				s.logger.Info("⏹️ 录制任务结束")
				return
			}

			// 生成录制文件名
			formatTime := time.Now().Local().Format("2006-01-02_15-04-05")
			segmentPath := filepath.Join(recordDir, formatTime+".mp4")
			segmentPathReal := filepath.Join(realDir, formatTime+".mp4")
			s.logger.Info("📽️ 开始录制分段（RTSP直录）", zap.String("file", segmentPath))

			// 直接用 RTSP 地址调用 FFmpeg 录制
			cmd := exec.CommandContext(s.ctx, "ffmpeg",
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
				"-an", // 不写音频避免封装失败
				"-t", fmt.Sprintf("%.0f", segment.Seconds()),
				"-y",
				segmentPath,
			)

			cmd.Stderr = os.Stderr

			//if err := cmd.Start(); err != nil {
			//	s.logger.Error("启动FFmpeg录制失败", zap.Error(err))
			//	return
			//}

			if err := cmd.Run(); err != nil {
				s.logger.Error("FFmpeg录制执行失败", zap.Error(err))
				return
			}

			s.recordFFmpegCmd = cmd
			s.recordStdin = nil // 不再需要手动写入

			segmentStart := time.Now().Unix()
			segmentEnd := time.Now().Add(segment)
			ticker := time.NewTicker(500 * time.Millisecond)
			defer ticker.Stop()

		WAIT_LOOP:
			for {
				select {
				case <-ticker.C:
					if !s.recordStatus.Load() || time.Now().Unix() >= s.recordEndTimestamp.Load() || time.Now().After(segmentEnd) {
						break WAIT_LOOP
					}
				case <-s.ctx.Done():
					s.logger.Info("录制任务中断：收到 context 退出信号")
					return
				}
			}

			// 停止当前段
			s.ClearRecorder() // 清理当前段资源

			s.logger.Info("✅ 分段录制完成", zap.String("file", segmentPath))

			// 推送视频记录
			segmentStop := time.Now().Unix()
			dto := StoreRecordVideoDto{
				PathURL:        segmentPathReal,
				StartTimestamp: segmentStart,
				EndTimestamp:   segmentStop,
				ID:             s.id,
			}

			go func(data StoreRecordVideoDto) {
				ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
				defer cancel()
				if err := detectStore.StoreRecordVideo(ctx, data); err != nil {
					s.logger.Error("🔴 推送视频记录失败", zap.Error(err))
				} else {
					s.logger.Info("✅ 已推送视频记录", zap.Any("video", data))
				}
			}(dto)
		}
	}()

	return nil
}

func (s *Session) StopRecording() {
	s.recordStatus.Store(false)
	s.ClearRecorder()
	s.logger.Info("📋 录制已手动停止")
}
