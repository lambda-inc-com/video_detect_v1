package engine

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"sync"
	"time"
)

type DetectionResult struct {
	X1    int     `json:"x1"`
	Y1    int     `json:"y1"`
	X2    int     `json:"x2"`
	Y2    int     `json:"y2"`
	Label string  `json:"label"`
	Conf  float64 `json:"conf"`
}

type DetectResponse struct {
	Success bool              `json:"success"`
	Count   int               `json:"count"`
	Results []DetectionResult `json:"results"`
	TimeMs  int               `json:"time_ms"`
	Error   string            `json:"error"`
}

var bufPool sync.Pool

func init() {
	bufPool = sync.Pool{New: func() interface{} { return new(bytes.Buffer) }}
}

// 监测对象
func detectObjects(data []byte, aiURL string) ([]DetectionResult, error) {
	if data == nil || len(data) == 0 {
		return nil, fmt.Errorf("图像识别 输入数据为空")
	}

	// 使用 bufPool 复用内存
	buf := bufPool.Get().(*bytes.Buffer)
	defer bufPool.Put(buf)
	buf.Reset()
	buf.Write(data)

	resp, err := http.Post(aiURL, "application/octet-stream", buf)
	if err != nil {
		return nil, fmt.Errorf("图像识别 请求失败: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		bodyBytes, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("图像识别 服务错误: %d, 响应: %s", resp.StatusCode, string(bodyBytes))
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("图像识别 响应读取失败: %w", err)
	}

	var detectResp DetectResponse
	if err := json.Unmarshal(body, &detectResp); err != nil {
		return nil, fmt.Errorf("图像识别 JSON解析失败: %w", err)
	}

	if !detectResp.Success {
		return nil, fmt.Errorf("图像识别 返回标记失败: %s", detectResp.Error)
	}

	return detectResp.Results, nil
}

// detectObjectsUvicronSocket 开启 UvicronSocket 减少 tcp损耗
func detectObjectsUvicronSocket(data []byte, socketPath, aiURL string) ([]DetectionResult, error) {
	buf := bufPool.Get().(*bytes.Buffer)
	defer bufPool.Put(buf)
	buf.Reset()
	buf.Write(data)

	// 创建 Unix Socket HTTP 客户端
	client := &http.Client{
		Transport: &http.Transport{
			DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
				return net.Dial("unix", socketPath)
			},
		},
		Timeout: 5 * time.Second, // 超时控制，可调
	}

	req, err := http.NewRequest("POST", aiURL, buf)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/octet-stream")

	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		bodyBytes, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("图像识别 服务错误: %d, 响应: %s", resp.StatusCode, string(bodyBytes))
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("图像识别 响应读取失败: %w", err)
	}

	var detectResp DetectResponse
	if err := json.Unmarshal(body, &detectResp); err != nil {
		return nil, fmt.Errorf("图像识别 JSON解析失败: %w", err)
	}

	if !detectResp.Success {
		return nil, fmt.Errorf("图像识别 返回标记失败: %s", detectResp.Error)
	}

	return detectResp.Results, nil
}

// ----- 检查资源 ------

// DetectServerStatusResp 识别服务资源情况
type DetectServerStatusResp struct {
	Success    bool     `json:"success"`
	CPUPercent float64  `json:"cpu_percent"` // 当前CPU总使用率 百分比
	CPUCount   int      `json:"cpu_count"`   // CPU核心数
	Memory     struct { // 内存
		TotalMB int     `json:"total_mb"` // 总内存 MB
		UsedMB  int     `json:"used_mb"`  // 使用内存 MB
		Percent float64 `json:"percent"`  // 百分比
	} `json:"memory"`
	HasGPU bool `json:"has_gpu"` // 是否有GPU
	GPU    struct {
		GPUUtilizationPercent int `json:"gpu_utilization_percent"` // GPU利用率
		GPUMemoryUsedMB       int `json:"gpu_memory_used_mb"`      // GPU 使用内存MB
		GPUMemoryTotalMB      int `json:"gpu_memory_total_mb"`     // GPU 总内存MB
	} `json:"gpu"`
}

// CheckDetectServiceResources 检查识别服务状态
func CheckDetectServiceResources(statusURL string) (bool, error) {
	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Get(statusURL)
	if err != nil {
		return false, fmt.Errorf("无法访问 识别服务器状态: %w", err)
	}
	defer resp.Body.Close()

	var status DetectServerStatusResp
	if err := json.NewDecoder(resp.Body).Decode(&status); err != nil {
		return false, fmt.Errorf("状态解析失败: %w", err)
	}

	if !status.Success {
		return false, fmt.Errorf("识别服务器返回失败状态")
	}

	fmt.Printf("识别服务器 状态: CPU %.1f%%, 核心 %d, 内存 %.1f%%, GPU: %v\n",
		status.CPUPercent, status.CPUCount, status.Memory.Percent, status.HasGPU)

	// 有 GPU，直接通过
	if status.HasGPU {
		fmt.Printf("GPU 使用率: %d%%, 显存: %d/%d MB\n",
			status.GPU.GPUUtilizationPercent, status.GPU.GPUMemoryUsedMB, status.GPU.GPUMemoryTotalMB)
		return true, nil
	}

	// CPU-only 判断逻辑
	if status.CPUCount <= 2 && status.CPUPercent > 50.0 {
		return false, nil
	}
	if status.CPUCount <= 4 && status.CPUPercent > 70.0 {
		return false, nil
	}
	if status.Memory.Percent > 80.0 {
		return false, nil
	}

	return true, nil
}

// CheckDetectHostServiceResources 检查识别服务状态
func CheckDetectHostServiceResources(statusURL string, maxUsedPercent float64) (bool, error) {
	if maxUsedPercent > 100.0 || maxUsedPercent <= 0 {
		maxUsedPercent = 90.0
	}

	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Get(statusURL)
	if err != nil {
		return false, fmt.Errorf("无法访问 识别服务器状态: %w", err)
	}
	defer resp.Body.Close()

	var status DetectServerStatusResp
	if err := json.NewDecoder(resp.Body).Decode(&status); err != nil {
		return false, fmt.Errorf("状态解析失败: %w", err)
	}

	if !status.Success {
		return false, fmt.Errorf("识别服务器返回失败状态")
	}

	fmt.Printf("识别服务器 状态: CPU %.1f%%, 核心 %d, 内存 %.1f%%, GPU: %v\n",
		status.CPUPercent, status.CPUCount, status.Memory.Percent, status.HasGPU)

	// 有 GPU，直接通过
	if status.HasGPU {
		fmt.Printf("GPU 使用率: %d%%, 显存: %d/%d MB\n",
			status.GPU.GPUUtilizationPercent, status.GPU.GPUMemoryUsedMB, status.GPU.GPUMemoryTotalMB)
		return true, nil
	}

	// CPU-only 判断逻辑

	if status.CPUPercent >= maxUsedPercent || status.Memory.Percent >= maxUsedPercent {
		return false, nil
	}

	return true, nil
}
