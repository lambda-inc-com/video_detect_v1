package wvp_utils

import (
	"errors"
	"fmt"
	jsoniter "github.com/json-iterator/go"
	"net/http"
)

type WVPPlayURLResp struct {
	Code int        `json:"code,omitempty" gorm:"column:code"`
	Msg  string     `json:"msg,omitempty" gorm:"column:msg"`
	Data WVPPlayURL `json:"data,omitempty" gorm:"column:data"`
}

type WVPPlayURL struct {
	App           string  `json:"app"`
	Stream        string  `json:"stream"`
	Ip            *string `json:"ip"`
	Flv           string  `json:"flv"`
	HttpsFlv      string  `json:"https_flv"`
	WsFlv         string  `json:"ws_flv"`
	WssFlv        string  `json:"wss_flv"`
	Fmp4          string  `json:"fmp4"`
	HttpsFmp4     string  `json:"https_fmp4"`
	WsFmp4        string  `json:"ws_fmp4"`
	WssFmp4       string  `json:"wss_fmp4"`
	Hls           string  `json:"hls"`
	HttpsHls      string  `json:"https_hls"`
	WsHls         string  `json:"ws_hls"`
	WssHls        string  `json:"wss_hls"`
	Ts            string  `json:"ts"`
	HttpsTs       string  `json:"https_ts"`
	WsTs          string  `json:"ws_ts"`
	WssTs         *string `json:"wss_ts"`
	Rtmp          string  `json:"rtmp"`
	Rtmps         *string `json:"rtmps"`
	Rtsp          string  `json:"rtsp"`
	Rtsps         *string `json:"rtsps"`
	Rtc           string  `json:"rtc"`
	Rtcs          string  `json:"rtcs"`
	MediaServerId string  `json:"mediaServerId"`
	MediaInfo     struct {
		App         string `json:"app"`
		Stream      string `json:"stream"`
		MediaServer struct {
			Id                string      `json:"id"`
			Ip                string      `json:"ip"`
			HookIp            string      `json:"hookIp"`
			SdpIp             string      `json:"sdpIp"`
			StreamIp          string      `json:"streamIp"`
			HttpPort          int         `json:"httpPort"`
			HttpSSlPort       int         `json:"httpSSlPort"`
			RtmpPort          int         `json:"rtmpPort"`
			FlvPort           int         `json:"flvPort"`
			FlvSSLPort        int         `json:"flvSSLPort"`
			WsFlvPort         int         `json:"wsFlvPort"`
			WsFlvSSLPort      int         `json:"wsFlvSSLPort"`
			RtmpSSlPort       int         `json:"rtmpSSlPort"`
			RtpProxyPort      int         `json:"rtpProxyPort"`
			RtspPort          int         `json:"rtspPort"`
			RtspSSLPort       int         `json:"rtspSSLPort"`
			AutoConfig        bool        `json:"autoConfig"`
			Secret            string      `json:"secret"`
			HookAliveInterval float64     `json:"hookAliveInterval"`
			RtpEnable         bool        `json:"rtpEnable"`
			Status            bool        `json:"status"`
			RtpPortRange      string      `json:"rtpPortRange"`
			SendRtpPortRange  string      `json:"sendRtpPortRange"`
			RecordAssistPort  int         `json:"recordAssistPort"`
			CreateTime        string      `json:"createTime"`
			UpdateTime        string      `json:"updateTime"`
			LastKeepaliveTime interface{} `json:"lastKeepaliveTime"`
			DefaultServer     bool        `json:"defaultServer"`
			RecordDay         int         `json:"recordDay"`
			RecordPath        string      `json:"recordPath"`
			Type              string      `json:"type"`
			TranscodeSuffix   interface{} `json:"transcodeSuffix"`
		} `json:"mediaServer"`
		Schema          string      `json:"schema"`
		ReaderCount     int         `json:"readerCount"`
		VideoCodec      string      `json:"videoCodec"`
		Width           int         `json:"width"`
		Height          int         `json:"height"`
		AudioCodec      interface{} `json:"audioCodec"`
		AudioChannels   interface{} `json:"audioChannels"`
		AudioSampleRate interface{} `json:"audioSampleRate"`
		Duration        interface{} `json:"duration"`
		Online          bool        `json:"online"`
		OriginType      int         `json:"originType"`
		AliveSecond     int         `json:"aliveSecond"`
		BytesSpeed      int         `json:"bytesSpeed"`
		CallId          string      `json:"callId"`
	} `json:"mediaInfo"`
	StartTime        interface{} `json:"startTime"`
	EndTime          interface{} `json:"endTime"`
	DownLoadFilePath interface{} `json:"downLoadFilePath"`
	TranscodeStream  interface{} `json:"transcodeStream"`
	Progress         float64     `json:"progress"`
}

type APIAuth struct {
	Url    string
	Token  string
	Client *http.Client
}

func NewAPIAuth(baseUrl, token string) APIAuth {
	return APIAuth{
		Url:    baseUrl,
		Token:  token,
		Client: http.DefaultClient,
	}
}

// 获取视频播放地址
func WvpStartPlay(playID string, apiAuth APIAuth) (string, error) {

	// 拼接请求 URL
	url := fmt.Sprintf("%s/api/play/start/%s", apiAuth.Url, playID)

	// 创建新的请求
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return "", errors.New(fmt.Sprintf("创建请求失败: %v", err))
	}

	// 添加 Authorization 头部
	req.Header.Add("Access-Token", apiAuth.Token)

	// 发起请求
	resp, err := apiAuth.Client.Do(req)
	if err != nil {
		return "", errors.New(fmt.Sprintf("请求开始播放失败: %v", err))
	}
	defer resp.Body.Close()

	// 检查 HTTP 响应状态码
	if resp.StatusCode != http.StatusOK {
		return "", errors.New(fmt.Sprintf("意外的状态码: %d", resp.StatusCode))
	}

	// 解析返回的 JSON 响应
	var result WVPPlayURLResp

	if err = jsoniter.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", errors.New(fmt.Sprintf("解析播放响应失败: %v", err))
	}

	// 返回播放结果
	return result.Data.Rtsp, nil
}
