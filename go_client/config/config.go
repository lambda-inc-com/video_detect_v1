package config

import (
	"flag"
	"github.com/pelletier/go-toml/v2"
	"os"
)

type Config struct {
	env    env    `toml:"env"`
	Server Server `toml:"server"`
	Engine Engine `toml:"engine"`
	Logger Logger `toml:"logger"`
	Redis  Redis  `toml:"redis"`
	Store  Store  `toml:"store"`

	HKVideo HKVideo `toml:"hk-video"`
}

type env struct {
	configPath string `toml:"config-path"`
}

type Server struct {
	UseH2C         bool   `toml:"use-h2c"`
	ListenHttpAddr string `toml:"listen-http-addr"`
	GrpcPeerAddr   string `toml:"grpc-peer-addr"`
}

type Engine struct {
	upperLimit          int    // 上限 % 0-100
	UvicornSocket       bool   `toml:"uvicorn-socket"`          // 开启Unix Socket 模式
	HealthyHeartbeat    int32  `toml:"healthy-heartbeat"`       // 会话健康检查时间 s
	CloseChanCap        int    `toml:"close-chan-cap"`          // 关闭识别流会话通道的缓存大小
	SocketPath          string `toml:"socket-path"`             // unix socket 地址
	DetectAIURL         string `toml:"detect-ai-url"`           // 识别请求URL
	PushUrlInternalPre  string `toml:"push-url-internal-pre"`   // 推流使用前缀 ：如 rtmp://rtmp-server/live/stream
	PushUrlPublicPre    string `toml:"push-url-public-pre"`     // 播放展示用：如 rtmp://mydomain.com/live/stream
	PushUrlPublicHlsPre string `toml:"push-url-public-hls-pre"` // 播放Hls展示用：如 http://mydomain.com/live/stream

	PullRestartMode string `toml:"pull-restart-mode"` // 拉流重启方式 hk,wvp
}

type Store struct {
	DetectResultChannelKey string `toml:"detect-result-channel-key"` // 识别结果推送Key
	RecordVideoChannelKey  string `toml:"record-video-channel-key"`  // 视频录制推送Key

	RecordPath     string `toml:"record-path"`      // 录制文件路径
	RecordPathReal string `toml:"record-path-real"` // 录制文件路径(系统真实路径)

	DetectResultPath     string `toml:"detect-result-path"`      // 识别结果路径(容器内路径)
	DetectResultPathReal string `toml:"detect-result-path-real"` // 识别结果路径(系统真实路径)
}

type Redis struct {
	Host         string `toml:"host"`
	Password     string `toml:"password"`
	MinIdleConns int    `toml:"min-idle-conns"`
	MaxIdleConns int    `toml:"max-idle-conns"`
}

type Logger struct {
	LocalTime    bool   `toml:"local-time"`     // 是否使用本地时间，默认使用UTC
	Compress     bool   `toml:"compress"`       // 是否使用GZIP格式压缩，默认不压缩
	SplitMaxSize int    `toml:"split-max-size"` // 日志最大存储大小（MB）
	MaxAge       int    `toml:"max-age"`        // 日志保存天数
	MaxBackups   int    `toml:"max-backups"`    // 志最大存储数量（会被MaxAge删除）
	LogPath      string `toml:"log-path"`       // 日志路径
	LogLevel     string `toml:"log-level"`      // 日志等级
}

func BindConfig(args []string) (*Config, error) {
	var config Config
	fs := flag.NewFlagSet("config", flag.ExitOnError)
	fs.StringVar(&config.env.configPath, "config-path", "./video_detect.toml", "detect config path")

	if err := fs.Parse(args[1:]); err != nil {
		return nil, err
	}

	open, err := os.Open(config.env.configPath)
	defer func() {
		_ = open.Close() // 确保文件在函数返回前被关闭
	}()

	if err != nil {
		return nil, err
	}

	if err = toml.NewDecoder(open).Decode(&config); err != nil {
		return nil, err
	}

	return &config, nil
}

type HKVideo struct {
	BaseURL   string `toml:"base-url"`
	AppKey    string `toml:"app-key"`
	AppSecret string `toml:"app-secret"`
}
