package hk_utils

import (
	"bytes"
	"crypto/hmac"
	"crypto/md5"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	urls "net/url"
	"sort"
	"strings"
	"time"
)

type APIAuth struct {
	Url       string
	AppKey    string
	AppSecret string
	Client    *http.Client
}

func NewAPIAuth(baseUrl, appKey, appSecret string) APIAuth {
	return APIAuth{
		Url:       baseUrl,
		AppKey:    appKey,
		AppSecret: appSecret,
		Client:    http.DefaultClient,
	}
}

// NewRequest 处理签名字符串构建和请求发送的函数
func (a *APIAuth) NewRequest(method, url string, body map[string]interface{}) (*http.Request, error) {
	jsonBody, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal JSON body: %v", err)
	}
	contentMD5 := CalculateContentMD5(jsonBody)

	headers := map[string]interface{}{
		"Accept":       "*/*",
		"Content-MD5":  contentMD5,
		"Content-Type": "application/json;charset=UTF-8",
		"Date":         time.Now().UTC().Format(http.TimeFormat),
		"X-Ca-Key":     a.AppKey,
	}

	// 准备签名字符串的 headers 和规范化 headers
	signatureHeaders, canonicalHeaders := PrepareHeaders(method, url, headers)

	// 生成 HMAC-SHA256 签名
	signature := CalculateHMACSHA256(canonicalHeaders, a.AppSecret)
	headers["X-Ca-Signature"] = signature
	headers["X-Ca-Signature-Headers"] = signatureHeaders

	// 构建 HTTP 请求
	req, err := http.NewRequest(method, url, bytes.NewBuffer(jsonBody))
	if err != nil {
		return nil, err
	}

	for k, v := range headers {
		req.Header.Set(k, fmt.Sprintf("%v", v))
	}

	return req, nil
}

// CalculateContentMD5 计算并返回 Content-MD5 值
func CalculateContentMD5(body []byte) string {
	hash := md5.New()
	_, err := hash.Write(body)
	if err != nil {
		fmt.Println("Failed to calculate MD5:", err)
		return ""
	}
	return base64.StdEncoding.EncodeToString(hash.Sum(nil))
}

// CalculateHMACSHA256 生成 HMAC-SHA256 签名
func CalculateHMACSHA256(data, secret string) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(data))
	return base64.StdEncoding.EncodeToString(mac.Sum(nil))
}

// PrepareHeaders 筛选并规范 headers
func PrepareHeaders(method, url string, headers map[string]interface{}) (string, string) {
	excludedHeaders := map[string]struct{}{
		"x-ca-signature":         {},
		"x-ca-signature-headers": {},
		"accept":                 {},
		"content-md5":            {},
		"content-type":           {},
		"date":                   {},
		"content-length":         {},
		"server":                 {},
		"connection":             {},
		"host":                   {},
		"transfer-encoding":      {},
		"x-application-context":  {},
		"content-encoding":       {},
	}

	SpecialHeaders := map[string]struct{}{
		"accept":       {},
		"content-md5":  {},
		"content-type": {},
		"date":         {},
	}

	flattenedHeaders := FlattenHeaders(headers)
	keys := make([]string, 0, len(flattenedHeaders))
	FKeys := make([]string, 0, len(flattenedHeaders))

	for k := range flattenedHeaders {
		keys = append(keys, k)
		if _, excluded := excludedHeaders[k]; !excluded {
			FKeys = append(FKeys, k)
		}
	}

	sort.Strings(keys)
	sort.Strings(FKeys)
	signatureHeaders := strings.Join(FKeys, ",")

	parsedURL, _ := urls.Parse(url)
	path := parsedURL.Path

	// 生成规范化的 headers 字符串
	var canonicalHeaders strings.Builder
	for _, k := range keys {
		if _, special := SpecialHeaders[k]; special {
			canonicalHeaders.WriteString(fmt.Sprintf("%v\n", flattenedHeaders[k]))
		} else {
			if _, ok := excludedHeaders[k]; !ok {
				canonicalHeaders.WriteString(fmt.Sprintf("%s:%v\n", k, flattenedHeaders[k]))
			}
		}
	}

	return signatureHeaders, fmt.Sprintf("%s\n%s%s", strings.ToUpper(method), canonicalHeaders.String(), path)
}

// FlattenHeaders 格式化 headers，保留类型
func FlattenHeaders(headers map[string]interface{}) map[string]interface{} {
	flattened := make(map[string]interface{})
	for k, v := range headers {
		lowerKey := strings.ToLower(k)
		if lowerKey != "x-ca-signature" && lowerKey != "x-ca-signature-headers" && v != "" {
			flattened[lowerKey] = v
		}
	}
	return flattened
}

// ConstructCanonicalURL 构造 URL
func ConstructCanonicalURL(url string, queryParams, bodyForm map[string]string) string {
	// 没有 query 和 bodyForm，直接返回 url
	return url
}

// mergeMaps 合并 Query 和 BodyForm 参数
func mergeMaps(maps ...map[string]interface{}) map[string]interface{} {
	merged := make(map[string]interface{})
	for _, m := range maps {
		for k, v := range m {
			merged[k] = v
		}
	}
	return merged
}

// GetHeaderValue 获取 header 值，保持原始类型
func GetHeaderValue(headers map[string]interface{}, key string) interface{} {
	if val, exists := headers[key]; exists {
		return val
	}
	return nil
}

func SetHeadersInOrder(req *http.Request, headers map[string]interface{}) {
	order := []string{
		"Accept", "Accept-Encoding", "Accept-Language", "Connection", "Content-Length",
		"Content-Type", "Cookie", "header-A", "header-B", "X-Ca-Key", "X-Ca-Signature",
		"X-Ca-Signature-Headers", "X-Ca-Timestamp", "X-Requested-With",
	}

	for _, key := range order {
		if val, exists := headers[key]; exists {
			req.Header.Set(key, fmt.Sprint(val)) // 保持各自类型
		}
	}
}
func ConvertToISO8601DayStartAndEnd(date time.Time) (string, string) {
	// Set time to the beginning of the day (00:00:00.000)
	startOfDay := time.Date(date.Year(), date.Month(), date.Day(), 0, 0, 0, 0, date.Location())
	// Set time to the end of the day (23:59:59.999)
	endOfDay := time.Date(date.Year(), date.Month(), date.Day(), 23, 59, 59, int(time.Millisecond*999), date.Location())

	const iso8601Format = "2006-01-02T15:04:05.000Z07:00"
	startOfDayStr := startOfDay.Format(iso8601Format)
	endOfDayStr := endOfDay.Format(iso8601Format)

	return startOfDayStr, endOfDayStr
}
