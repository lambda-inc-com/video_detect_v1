package hk_utils

import (
	"crypto/tls"
	"errors"
	"fmt"
	jsoniter "github.com/json-iterator/go"
	"net/http"
)

type HKPlayURL struct {
	Url string `json:"url,omitempty" gorm:"column:url"` // 视频地址
}

type HKPlayURLResponse struct {
	Code string    `json:"code,omitempty" gorm:"column:code"`
	Msg  string    `json:"msg,omitempty" gorm:"column:msg"`
	Data HKPlayURL `json:"data,omitempty" gorm:"column:data"`
}

func GetStartPlayUrl(deviceId, protocol string, apiAuth APIAuth) (string, error) {

	body := map[string]interface{}{
		"cameraIndexCode": deviceId,
		"protocol":        protocol,
	}

	url := fmt.Sprintf("%s/api/video/v2/cameras/previewURLs", apiAuth.Url)

	req, err := apiAuth.NewRequest("POST", url, body)
	if err != nil {
		return "", err
	}

	apiAuth.Client.Transport = &http.Transport{
		TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
	}

	resp, err := apiAuth.Client.Do(req)
	if err != nil {
		return "", errors.New(fmt.Sprintf("请求失败: %v", err))
	}
	defer resp.Body.Close()

	var result HKPlayURLResponse
	if err = jsoniter.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", errors.New(fmt.Sprintf("解析视频列表响应失败: %v", err))
	}

	if result.Code != "0" {
		return "", errors.New(fmt.Sprintf("请求执行失败: %v", err))
	}

	return result.Data.Url, nil
}
