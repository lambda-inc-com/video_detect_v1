package engine

import (
	"go_client/config"
	"go_client/pkg/hk_utils"
	"go_client/pkg/wvp_utils"
)

// PullStreamEOFRestart 拉流EOF重启
type PullStreamEOFRestart interface {
	ReGetRtspURL(sessionID string) (string, error)
}

type GetRtspURL func(sessionID string) (string, error)

func (g GetRtspURL) ReGetRtspURL(sessionID string) (string, error) {
	return g(sessionID)
}

func GetHkRtspUrl(cfg *config.Config) GetRtspURL {
	return func(sessionID string) (string, error) {
		return hk_utils.GetStartPlayUrl(
			sessionID,
			"rtsp",
			hk_utils.NewAPIAuth(cfg.HKVideo.BaseURL, cfg.HKVideo.AppKey, cfg.HKVideo.AppSecret),
		)
	}
}

func GetWVPRtspUrl(cfg *config.Config) GetRtspURL {
	return func(sessionID string) (string, error) {
		return wvp_utils.WvpStartPlay(
			sessionID,
			wvp_utils.NewAPIAuth(cfg.WVPVideo.BaseURL, cfg.WVPVideo.Token),
		)
	}
}
