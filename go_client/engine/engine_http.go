package engine

import (
	"encoding/base64"
	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
	"go_client/config"
	"go_client/pkg/result"
	"go_client/pkg/status"
	"gocv.io/x/gocv"
	"image"
	"image/color"
	"io"
	"net/http"
	"time"
)

type DetectHTTPService interface {
	CreateSession(c *gin.Context) error      // 创建识别会话
	GetAllSessionDesc(c *gin.Context) error  // 获取会话描述列表
	GetSessionDescByID(c *gin.Context) error // 根据ID获取会话描述

	StartDetect(c *gin.Context) error // 开始识别（并判断是否创建拉流Session）
	StopDetect(c *gin.Context) error  // 暂停识别（仍保持推流）

	StartRecord(c *gin.Context) error // 开始录制（并判断是否创建拉流Session）
	StopRecord(c *gin.Context) error  // 结束录制

	RemoveSession(c *gin.Context) error // 删除会话并停止拉流推流
	DetectTest(c *gin.Context) error    // 测试
}

func RegisterDetectHTTPService(eng *gin.Engine, srv DetectHTTPService) {
	eng.POST("/test", WrapHandler(srv.DetectTest))

	detect := eng.Group("/session/v1")
	{

		detect.GET("/list", WrapHandler(srv.GetAllSessionDesc))

		action := detect.Group("/:sessionID")
		{
			action.POST("", WrapHandler(srv.CreateSession))
			action.GET("", WrapHandler(srv.GetSessionDescByID))
			action.PUT("/detect/stop", WrapHandler(srv.StopDetect))
			action.PUT("/detect/start", WrapHandler(srv.StartDetect))

			action.PUT("/record/stop", WrapHandler(srv.StopRecord))
			action.PUT("/record/start", WrapHandler(srv.StartRecord))
			action.DELETE("", WrapHandler(srv.RemoveSession))
		}
	}
}

// DetectHTTPServiceV1 识别引擎http服务
type DetectHTTPServiceV1 struct {
	cfg     *config.Config
	manager *SessionManager
	logger  *zap.Logger
}

func NewDetectHTTPServiceV1(cfg *config.Config, manager *SessionManager, logger *zap.Logger) DetectHTTPService {
	return DetectHTTPServiceV1{cfg, manager, logger}
}

func (d DetectHTTPServiceV1) DetectTest(c *gin.Context) error {
	// 1. 解析上传的文件
	file, err := c.FormFile("file")
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "请上传文件"})
		return nil
	}

	openedFile, err := file.Open()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "文件打开失败"})
		return nil
	}
	defer openedFile.Close()

	imgBytes, err := io.ReadAll(openedFile)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "读取文件失败"})
		return nil
	}

	// 2. 调用 detectObjects
	aiURL := d.cfg.Engine.DetectAIURL
	results, err := detectObjects(imgBytes, aiURL)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return nil
	}

	// 3. 转换为 gocv.Mat
	imgMat, err := gocv.IMDecode(imgBytes, gocv.IMReadColor)
	if err != nil || imgMat.Empty() {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "图像解码失败"})
		return nil
	}
	defer imgMat.Close()

	// 4. 画识别框
	for _, r := range results {
		rect := image.Rect(r.X1, r.Y1, r.X2, r.Y2)
		_ = gocv.Rectangle(&imgMat, rect, color.RGBA{0, 255, 0, 0}, 2)
		_ = gocv.PutText(&imgMat, r.Label, image.Pt(r.X1, r.Y1-10),
			gocv.FontHersheyPlain, 1.2, color.RGBA{255, 0, 0, 0}, 2)
	}

	// 5. 编码结果为 JPEG 并转为 base64
	resultImgBytes, err := gocv.IMEncode(gocv.JPEGFileExt, imgMat)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "图像编码失败"})
		return nil
	}

	base64Str := base64.StdEncoding.EncodeToString(resultImgBytes.GetBytes())

	// 6. 返回 JSON
	c.JSON(http.StatusOK, gin.H{
		"message":      "检测完成",
		"results":      results,
		"image_base64": "data:image/jpeg;base64," + base64Str,
	})
	return nil
}

func (d DetectHTTPServiceV1) CreateSession(c *gin.Context) error {
	var action SessionAction
	if err := BindParams(&action, c.Params); err != nil {
		return err
	}

	var req CreateSessionReq
	err := Bind(&req, c.Request.Body)
	if err != nil {
		return err
	}

	// 创建会话会先判断是否存在，若不存在则创建
	_desc, exists := d.manager.GetSessionDescByID(action.SessionID)
	if exists {
		result.New(http.StatusOK).
			Data(SessionExistenceResp{true, _desc}).
			Ok(c.Writer)
		return nil
	}

	session, err := d.manager.CreateSession(action.SessionID, req.RtspURL, d.cfg.Engine.DetectAIURL,
		SetSessionVideoStreamConfig(req.Width, req.Height, req.Framerate))
	if err != nil {
		return status.Wrapper(http.StatusInternalServerError, err)
	}

	desc := session.GetDesc(d.manager.pushUrlPublicPre, d.manager.pushUrlPublicHlsPre)

	result.New(http.StatusOK).
		Data(SessionExistenceResp{false, desc}).
		Ok(c.Writer)
	return nil
}

func (d DetectHTTPServiceV1) GetAllSessionDesc(c *gin.Context) error {
	descList := d.manager.GetSessionDescList()
	result.New(http.StatusOK).Data(descList).Ok(c.Writer)
	return nil
}

func (d DetectHTTPServiceV1) GetSessionDescByID(c *gin.Context) error {
	var action SessionAction
	if err := BindParams(&action, c.Params); err != nil {
		return err
	}
	desc, ok := d.manager.GetSessionDescByID(action.SessionID)

	result.New(http.StatusOK).
		Data(SessionExistenceResp{ok, desc}).
		Ok(c.Writer)
	return nil
}

func (d DetectHTTPServiceV1) StopDetect(c *gin.Context) error {
	var action SessionAction
	if err := BindParams(&action, c.Params); err != nil {
		return err
	}

	err := d.manager.StopSessionDetect(action.SessionID)
	if err != nil {
		return status.Wrapper(http.StatusInternalServerError, err)
	}

	result.New(http.StatusOK).Data(gin.H{"ok": true}).Ok(c.Writer)
	return nil
}

func (d DetectHTTPServiceV1) StartDetect(c *gin.Context) error {
	var action SessionAction
	if err := BindParams(&action, c.Params); err != nil {
		return err
	}

	var req StartDetectReq
	if err := Bind(&req, c.Request.Body); err != nil {
		return err
	}

	// 创建会话会先判断是否存在，若不存在则创建
	session, exists := d.manager.sessions.Load(action.SessionID)
	if !exists {
		// 创建会话
		_session, err := d.manager.CreateSession(
			action.SessionID,
			req.RtspURL,
			d.cfg.Engine.DetectAIURL,
			SetSessionVideoStreamConfig(req.Width, req.Height, req.Framerate),
			SetPullerEOFAutoRestart(req.RetryTimes, req.EOFAutoRestart),
		)
		if err != nil {
			return status.Wrapper(http.StatusInternalServerError, err)
		}
		session = _session

		// 开启识别
		err = d.manager.StartSessionDetect(session.id, req.EndTimestamp)
		if err != nil {
			return status.Wrapper(http.StatusInternalServerError, err)
		}

	} else {
		session.detectStatus.Store(true)
		session.detectEndTimestamp.Store(req.EndTimestamp)
	}

	desc := session.GetDesc(d.manager.pushUrlPublicPre, d.manager.pushUrlPublicHlsPre)
	result.New(http.StatusOK).Data(desc).Ok(c.Writer)
	return nil

}

func (d DetectHTTPServiceV1) StopRecord(c *gin.Context) error {
	var action SessionAction
	if err := BindParams(&action, c.Params); err != nil {
		return err
	}

	err := d.manager.StopSessionRecord(action.SessionID)
	if err != nil {
		return status.Wrapper(http.StatusInternalServerError, err)
	}

	result.New(http.StatusOK).Data(gin.H{"ok": true}).Ok(c.Writer)
	return nil
}

func (d DetectHTTPServiceV1) StartRecord(c *gin.Context) error {
	var action SessionAction
	if err := BindParams(&action, c.Params); err != nil {
		return err
	}

	var req StartRecordReq
	if err := Bind(&req, c.Request.Body); err != nil {
		return err
	}

	// 创建会话会先判断是否存在，若不存在则创建
	session, exists := d.manager.sessions.Load(action.SessionID)
	if !exists {
		// 创建会话
		_session, err := d.manager.CreateSession(
			action.SessionID,
			req.RtspURL,
			d.cfg.Engine.DetectAIURL,
			SetSessionVideoStreamConfig(req.Width, req.Height, req.Framerate),
			SetPullerEOFAutoRestart(req.RetryTimes, req.EOFAutoRestart),
		)
		if err != nil {
			return status.Wrapper(http.StatusInternalServerError, err)
		}
		session = _session
	}

	err := d.manager.StartSessionRecord(
		session.id,
		req.EndTimestamp,
		time.Second*time.Duration(req.SegmentedSec),
	)
	if err != nil {
		return status.Wrapper(http.StatusInternalServerError, err)
	}

	desc := session.GetDesc(d.manager.pushUrlPublicPre, d.manager.pushUrlPublicHlsPre)
	result.New(http.StatusOK).Data(desc).Ok(c.Writer)
	return nil

}

func (d DetectHTTPServiceV1) RemoveSession(c *gin.Context) error {
	var action SessionAction
	if err := BindParams(&action, c.Params); err != nil {
		return err
	}

	d.manager.RemoveSession(action.SessionID)

	result.New(http.StatusOK).Data(gin.H{"ok": true}).Ok(c.Writer)
	return nil
}
