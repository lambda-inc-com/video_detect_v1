package engine

import (
	"context"
	"fmt"
	"github.com/patrickmn/go-cache"
	"go.uber.org/zap"
	"gocv.io/x/gocv"
	"image"
	"image/color"
	"io"
	"net"
	"os/exec"
	"strings"
	"sync"
	"sync/atomic"
	"time"
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

// Session 流会话
type Session struct {
	width     int //  宽
	height    int //  高
	framerate int // 帧率

	id        string // 唯一标识
	streamKey string // 用于拼接 RTMP 推流地址
	rtspURL   string // 拉流链接

	detectEndTimestamp atomic.Int64 // 识别结束时间戳
	recordEndTimestamp atomic.Int64 // 录制结束时间戳

	detectStatus  atomic.Bool // 识别状态 false 停止 true 识别中
	recordStatus  atomic.Bool // 录制状态 false 停止 true 录制中
	runningStatus atomic.Bool // 运行状态 false 关闭 true 运行中

	handledClose atomic.Bool
	ctx          context.Context
	cancelFunc   context.CancelFunc
	logger       *zap.Logger

	closeCh chan<- string

	pullFFmpegCmd *exec.Cmd      // FFmpeg 拉流Cmd
	pullReader    io.Reader      // 拉流Reader
	ffmpegStdin   io.WriteCloser //  FFmpeg 推流进程的 stdin 管道
	pushFFmpegCmd *exec.Cmd      // FFmpeg 推流Cmd

	recordFFmpegCmd *exec.Cmd      // 录制FFmpegCmd
	recordStdin     io.WriteCloser // 录制 stdin 管道

	resultCache       *DetectionResultCache
	frameForDetection chan []byte

	localCache *cache.Cache // 内部缓存
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

func NewSessionWithCtx(id, rtsp string, ctx context.Context, cancelFunc context.CancelFunc, logger *zap.Logger, closeCh chan<- string, options ...SetSessionOption) *Session {
	s := &Session{
		detectStatus: atomic.Bool{},
		//rwLock:        new(sync.RWMutex),
		runningStatus: atomic.Bool{},
		ctx:           ctx,
		cancelFunc:    cancelFunc,
		id:            id,
		closeCh:       closeCh,
		rtspURL:       rtsp,
		logger:        logger,
		localCache:    cache.New(5*time.Minute, 10*time.Minute), // 默认超时5分钟，清理周期10分钟,
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
	if s.pullFFmpegCmd != nil && s.pullFFmpegCmd.Process != nil {
		_ = s.pullFFmpegCmd.Process.Kill()
		_ = s.pullFFmpegCmd.Wait()
	}
	s.pullFFmpegCmd = nil
	s.pullReader = nil

	// 停止推流 FFmpeg 进程
	if s.pushFFmpegCmd != nil && s.pushFFmpegCmd.Process != nil {
		_ = s.pushFFmpegCmd.Process.Kill()
		_ = s.pushFFmpegCmd.Wait()
	}
	s.pushFFmpegCmd = nil

	// 关闭 FFmpeg stdin 写入管道
	if s.ffmpegStdin != nil {
		_ = s.ffmpegStdin.Close()
	}
	s.ffmpegStdin = nil

	if s.recordFFmpegCmd != nil && s.recordFFmpegCmd.Process != nil {
		_ = s.recordFFmpegCmd.Process.Kill()
		_ = s.recordFFmpegCmd.Wait()
	}
	s.recordFFmpegCmd = nil

	// 清空上下文和控制函数
	s.ctx = nil
	s.cancelFunc = nil

	// 清空基本信息
	s.id = ""
	s.streamKey = ""
	s.rtspURL = ""

	s.resultCache = &DetectionResultCache{}

	s.localCache.Flush() // 只删除缓存不置空 防止频繁创建对象

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

func (s *Session) PrepareStream(pushRTMPURL string) (err error) {
	s.runningStatus.Store(true)
	s.detectStatus.Store(false) // 预准备时不识别 需要手动开启识别
	s.logger.Info(fmt.Sprintf("📽️ Session starting: url=%s, res=%dx%d, fps=%d", s.rtspURL, s.width, s.height, s.framerate))

	cleanup := func() {
		if s.pullFFmpegCmd != nil {
			_ = s.pullFFmpegCmd.Process.Kill()
			_ = s.pullFFmpegCmd.Wait()
		}
		if s.ffmpegStdin != nil {
			_ = s.ffmpegStdin.Close()
		}
		if s.pushFFmpegCmd != nil {
			_ = s.pushFFmpegCmd.Process.Kill()
			_ = s.pushFFmpegCmd.Wait()
		}
	}

	defer func() {
		if err != nil {
			cleanup() // 仅在出错时回收
		}
	}()

	// 启动拉流 FFmpeg（RTSP → stdout）
	pullCmd, stdout, err := startFFmpegReader(s.rtspURL, s.width, s.height, s.framerate)
	if err != nil {
		return fmt.Errorf("FFmpeg 拉流失败: %w", err)
	}
	s.pullFFmpegCmd = pullCmd
	s.pullReader = stdout

	// 启动推流 FFmpeg（stdin → RTMP）
	pushCmd, pushIO, err := startFFmpegPusher(s.width, s.height, float64(s.framerate), false, pushRTMPURL, s.logger)
	if err != nil {
		return fmt.Errorf("FFmpeg 推流失败: %w", err)
	}
	s.ffmpegStdin = pushIO
	s.pushFFmpegCmd = pushCmd

	s.logger.Info("拉流与推流 FFmpeg 初始化完成")
	return nil
}

func (s *Session) Run(aiDetectAIURL string, uvicornSocket bool, socketPath string) {
	defer func() {
		if r := recover(); r != nil {
			s.logger.Error("❌ Panic recovered in Run", zap.Any("error", r))
		}

		// 关闭资源
		s.runningStatus.Store(false)
		if s.pullFFmpegCmd != nil && s.pullFFmpegCmd.Process != nil {
			_ = s.pullFFmpegCmd.Process.Kill()
			_ = s.pullFFmpegCmd.Wait()
		}
		if s.ffmpegStdin != nil {
			_ = s.ffmpegStdin.Close()
		}
		if s.pushFFmpegCmd != nil && s.pushFFmpegCmd.Process != nil {
			_ = s.pushFFmpegCmd.Wait()
		}

		s.recordEndTimestamp = atomic.Int64{}
		if s.recordStdin != nil {
			_ = s.recordStdin.Close()
		}
		if s.recordFFmpegCmd != nil && s.recordFFmpegCmd.Process != nil {
			_ = s.recordFFmpegCmd.Process.Kill()
			_ = s.recordFFmpegCmd.Wait()
		}

		s.detectStatus.Store(false)
		s.recordStatus.Store(false)

		s.SendIDToCloseCh()
		s.logger.Info("📴 Stream session stopped")
	}()

	s.resultCache = &DetectionResultCache{}
	imgBuf := make([]byte, s.width*s.height*3)
	img := gocv.NewMat()
	defer img.Close()

	// 异步识别 goroutine
	go s.asyncDetectLoop(uvicornSocket, socketPath, aiDetectAIURL)

	lastDetect := time.Now()
	detectInterval := time.Second / 5 // 每秒识别 5 帧

	for {
		select {
		case <-s.ctx.Done():
			s.logger.Info("🛑 Context canceled")
			return
		default:
			if !s.runningStatus.Load() {
				return
			}

			_, err := io.ReadFull(s.pullReader, imgBuf)
			if err != nil {
				if err == io.EOF {
					s.logger.Error("检测到 EOF，流断开，终止", zap.String("id", s.id))
					s.cancelFunc()
					return
				}

				// 非EOF错误继续
				s.logger.Info("读取帧错误，跳过当前帧", zap.String("id", s.id), zap.Error(err))
				continue

			}

			if imgTmp, err := gocv.NewMatFromBytes(s.height, s.width, gocv.MatTypeCV8UC3, imgBuf); err == nil && !imgTmp.Empty() {
				img.Close()
				img = imgTmp
			} else {
				continue
			}

			// 写入录制
			if s.recordStatus.Load() && s.recordStdin != nil {
				_, err := s.recordStdin.Write(imgBuf)
				if err != nil {
					s.logger.Error("写入录制失败", zap.Error(err))
				}
			}

			var latestResults []DetectionResult
			func() {
				s.resultCache.RLock()
				defer s.resultCache.RUnlock()
				latestResults = append([]DetectionResult{}, s.resultCache.Results...)
			}()

			// 应用副本的识别结果
			for _, r := range latestResults {
				rect := image.Rect(r.X1, r.Y1, r.X2, r.Y2)
				_ = gocv.Rectangle(&img, rect, color.RGBA{0, 255, 0, 0}, 2)
				_ = gocv.PutText(&img, r.Label, image.Pt(r.X1, r.Y1-10),
					gocv.FontHersheyPlain, 1.2, color.RGBA{255, 0, 0, 0}, 2)
			}

			// 控制识别频率（基于时间）
			if s.detectStatus.Load() && time.Since(lastDetect) >= detectInterval {
				lastDetect = time.Now()
				func() {
					s.resultCache.Lock()
					defer s.resultCache.Unlock()
					s.resultCache.Results = nil
				}()

				imgBytes, err := gocv.IMEncode(gocv.JPEGFileExt, img) // ✅ 正确，JPEG 格式
				if err != nil {
					s.logger.Error("图像编码失败", zap.Error(err))
					continue // 跳过这一帧
				}
				select {
				case s.frameForDetection <- imgBytes.GetBytes():
				default:
					s.logger.Info("识别队列已满，跳过当前帧")
				}
			}

			// 推送给 FFmpeg 推流进程
			if _, err := s.ffmpegStdin.Write(img.ToBytes()); err != nil {
				s.logger.Error(fmt.Sprintf("[-] sessionID:%s 写入推流失败", s.id), zap.Error(err))
				s.cancelFunc()
				return
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

func (s *Session) asyncDetectLoop(uvicornSocket bool, socketPath string, aiDetectAIURL string) {
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
				// todo 存储结果

			}

			func() {
				s.resultCache.Lock()
				defer s.resultCache.Unlock()
				s.resultCache.Results = results
			}()

		}
	}
}

func (s *Session) StartRecording(recordDir string, segment time.Duration) error {
	s.logger.Info("▶️ 开始录制", zap.String("dir", recordDir), zap.Int64("recordEndTimestamp", s.recordEndTimestamp.Load()), zap.Duration("segment", segment))

	segmentIndex := 0
	s.recordStatus.Store(true)

	go func() {
		defer func() {
			s.recordStatus.Store(false)
			s.recordEndTimestamp.Store(0)
			if s.recordStdin != nil {
				_ = s.recordStdin.Close()
			}
			if s.recordFFmpegCmd != nil && s.recordFFmpegCmd.Process != nil {
				_ = s.recordFFmpegCmd.Process.Kill()
				_ = s.recordFFmpegCmd.Wait()
			}
		}()

		bufSize := s.width * s.height * 3
		buf := make([]byte, bufSize)

		for {
			if !s.recordStatus.Load() || time.Now().Unix() >= s.recordEndTimestamp.Load() {
				s.logger.Info("录制任务结束")
				return
			}

			segmentIndex++
			segmentPath := fmt.Sprintf("%s/%s.mp4", recordDir, time.Now().Local().Format("2006-01-02_15-04-05"))
			s.logger.Info("📽️ 开始录制分段", zap.String("file", segmentPath))

			cmd, stdin, err := startFFmpegRecording(segmentPath, s.width, s.height, s.framerate)

			s.recordFFmpegCmd = cmd
			s.recordStdin = stdin
			if err != nil {
				s.logger.Error("录制失败", zap.Error(err))
				return
			}

			segmentEnd := time.Now().Add(segment)
		WRITE_LOOP:
			for {
				select {
				case <-s.ctx.Done():
					s.logger.Info("录制收到 context 退出信号")
					return
				default:
					if !s.recordStatus.Load() || time.Now().Unix() >= s.recordEndTimestamp.Load() || time.Now().After(segmentEnd) {
						// 每段结束
						break WRITE_LOOP
					}
					n, err := io.ReadFull(s.pullReader, buf)
					if err != nil {
						if err == io.EOF {
							s.logger.Warn("拉流结束，停止录制")
							return
						}
						s.logger.Error("读取拉流失败", zap.Error(err))
						continue
					}
					if s.recordFFmpegCmd.ProcessState != nil && s.recordFFmpegCmd.ProcessState.Exited() {
						s.logger.Warn("录制进程已退出")
						return
					}
					_, _ = stdin.Write(buf[:n])
				}
			}

			_ = stdin.Close()
			_ = cmd.Process.Kill()
			_ = cmd.Wait()
			s.logger.Info("✅ 分段录制完成", zap.String("file", segmentPath))
		}
	}()

	return nil
}

// StopRecording 停止录制
func (s *Session) StopRecording() {
	if s.recordFFmpegCmd != nil && s.recordFFmpegCmd.Process != nil {
		_ = s.recordFFmpegCmd.Process.Kill()
		_ = s.recordFFmpegCmd.Wait()
		s.logger.Info("📋 录制已手动停止")
	}
	if s.recordStdin != nil {
		_ = s.recordStdin.Close()
	}
	s.recordStatus.Store(false)
	s.recordEndTimestamp.Store(0)
	s.recordFFmpegCmd = nil
	s.recordStdin = nil
}
