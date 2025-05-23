# Video Detect V1

一个基于Go和Python的视频流目标检测系统，支持实时视频流的目标检测、录制和推流功能。

## 功能特性

- 支持RTSP视频流接入
- 实时目标检测（基于YOLOv8）
- 支持视频流录制
- RTMP推流和HLS播放
- 支持海康威视和WVP视频平台接入
- HTTP和gRPC双协议接口
- 分布式架构设计
- 完善的日志系统
- Docker容器化部署

## 系统架构

系统由三个主要组件构成：

1. **Go Client服务**
   - 负责视频流的拉取、处理和推流
   - 提供HTTP和gRPC接口
   - 管理视频会话
   - 处理视频录制

2. **AI Service服务**
   - 基于FastAPI的Python服务
   - 使用YOLOv8进行目标检测
   - 支持多种目标类别识别

3. **RTMP Server**
   - 提供RTMP推流服务
   - 支持HLS播放

## 技术栈

- Go 1.24
- Python 3.10
- FastAPI
- YOLOv8
- OpenCV
- FFmpeg
- Redis
- Docker
- gRPC
- Gin Web Framework

## 安装部署

### 环境要求

- Docker & Docker Compose
- NVIDIA GPU (可选，用于AI加速)

### 快速开始

1. 克隆项目
```bash
git clone [项目地址]
cd video_detect_v1
```

2. 配置
编辑 `configs/video_detect.toml` 配置文件，根据实际环境修改相关配置。

3. 构建和启动服务
```bash
cd build
docker-compose up -d
```

服务将在以下端口启动：
- Go Client: 8080 (HTTP), 8081 (gRPC)
- AI Service: 5000
- RTMP Server: 1935 (RTMP), 8082 (HLS)

## 使用说明

### API接口

#### HTTP接口

1. 创建检测会话
```http
POST http://localhost:8080/api/v1/detect/session
Content-Type: application/json

{
    "streamUrl": "rtsp://example.com/stream",
    "detectStatus": true,
    "recordStatus": false
}
```

2. 控制检测状态
```http
PUT http://localhost:8080/api/v1/detect/session/{sessionId}/status
Content-Type: application/json

{
    "detectStatus": true,
    "recordStatus": true
}
```

3. 获取会话信息
```http
GET http://localhost:8080/api/v1/detect/session/{sessionId}
```

### 视频流访问

- RTMP播放地址：`rtmp://localhost:1935/live/{streamKey}`
- HLS播放地址：`http://localhost:8082/hls/{streamKey}.m3u8`

## 配置说明

主要配置项（`configs/video_detect.toml`）：

```toml
[server]
listen-http-addr = "0.0.0.0:8080"  # HTTP服务监听地址
grpc-peer-addr = "0.0.0.0:8081"    # gRPC服务监听地址

[engine]
detect-ai-url = "http://ai-service:5000/detect"  # AI服务地址
push-url-internal-pre = "rtmp://rtmp-server/live"  # 内部推流地址
push-url-public-pre = "rtmp://localhost:1935/live"  # 公网推流地址
```

## 开发说明

### 项目结构

```
.
├── ai_service/          # AI检测服务
├── go_client/          # Go客户端服务
│   ├── cmd/            # 主程序入口
│   ├── config/         # 配置管理
│   ├── engine/         # 核心引擎
│   └── pkg/            # 公共包
├── build/              # 构建相关文件
│   ├── Dockerfile-*    # Docker构建文件
│   └── docker-compose.yaml
└── configs/            # 配置文件
```

### 开发环境设置

1. Go开发环境
```bash
cd go_client
go mod tidy
go run cmd/main.go
```

2. Python开发环境
```bash
cd ai_service
pip install -r requirements.txt
uvicorn main:app --reload
```

## 注意事项

1. 确保服务器有足够的存储空间用于视频录制
2. 建议使用GPU加速AI检测服务
3. 注意配置文件中的敏感信息（如API Token）安全
4. 生产环境部署时建议使用HTTPS

5. **TODO 待优化项**
   - [ ] 系统资源检查
     - 在开启识别前检查系统资源（CPU、内存、GPU）使用情况
     - 设置资源使用阈值，超过阈值时拒绝开启新的识别任务
     - 实现资源监控和自动告警机制
     - 支持动态调整识别频率以平衡系统负载
     - 添加资源使用统计和预测功能
   - [ ] 其他优化项
     - 完善gRPC接口实现
     - 添加更多的单元测试
     - 优化日志记录策略
     - 实现更细粒度的权限控制

## 许可证

[待定]

## 贡献指南

欢迎提交Issue和Pull Request。

## 联系方式

[待补充]

## Go Client 详细说明

### 核心模块

1. **Engine模块** (`go_client/engine/`)
   - `engine.go`: 核心引擎实现，负责服务的启动、停止和生命周期管理
   - `session.go`: 会话管理，处理视频流的拉取、处理和推流
   - `detect.go`: 检测服务实现，包括HTTP和gRPC接口
   - `store.go`: 存储管理，处理视频录制和检测结果的存储

2. **配置管理** (`go_client/config/`)
   - `config.go`: 配置结构定义和加载
   - 支持TOML格式配置文件
   - 包含服务器、引擎、日志、Redis等配置项

3. **日志系统** (`go_client/pkg/logger/`)
   - 基于zap日志库
   - 支持日志分割和轮转
   - 可配置日志级别和存储策略

### 关键流程

1. **视频流处理流程**
   ```
   RTSP拉流 -> FFmpeg解码 -> OpenCV处理 -> AI检测 -> 绘制检测框 -> FFmpeg编码 -> RTMP推流
   ```

2. **会话管理流程**
   - 创建会话：生成唯一会话ID和流密钥
   - 启动检测：初始化FFmpeg进程，建立视频处理管道
   - 状态控制：支持暂停/恢复检测和录制
   - 会话清理：自动清理过期会话和资源

3. **检测结果处理**
   - 实时推送：通过Redis发布检测结果
   - 结果存储：保存检测结果到文件系统
   - 视频录制：支持检测触发录制

### 重要接口

1. **HTTP接口** (`go_client/engine/detect.go`)
   ```go
   // 会话管理
   POST   /api/v1/detect/session          // 创建检测会话
   PUT    /api/v1/detect/session/{id}     // 更新会话配置
   DELETE /api/v1/detect/session/{id}     // 删除会话
   GET    /api/v1/detect/session/{id}     // 获取会话信息
   
   // 状态控制
   PUT    /api/v1/detect/session/{id}/status  // 控制检测/录制状态
   GET    /api/v1/detect/session/{id}/status  // 获取当前状态
   
   // 检测结果
   GET    /api/v1/detect/session/{id}/results // 获取检测结果
   ```

2. **gRPC接口(尚未完善，若需要按照http和下面例子完善即可)** (`go_client/pb/`)
   ```protobuf
   service DetectService {
     rpc CreateSession(CreateSessionRequest) returns (SessionResponse);
     rpc UpdateSession(UpdateSessionRequest) returns (SessionResponse);
     rpc DeleteSession(DeleteSessionRequest) returns (Empty);
     rpc GetSession(GetSessionRequest) returns (SessionResponse);
     rpc StreamDetectResults(StreamRequest) returns (stream DetectResult);
   }
   ```

### 关键配置项

```toml
[engine]
# 视频处理配置
healthy-heartbeat = 60        # 会话健康检查间隔(秒)
close-chan-cap = 128         # 关闭通道缓冲区大小
pull-restart-mode = "hk"     # 拉流重启模式(hk/wvp)

# 推流地址配置
push-url-internal-pre = "rtmp://rtmp-server/live"    # 内部推流地址
push-url-public-pre = "rtmp://localhost:1935/live"   # 公网推流地址
push-url-public-hls-pre = "http://localhost:1935"    # HLS播放地址

# AI服务配置
detect-ai-url = "http://ai-service:5000/detect"      # AI服务地址
```

### 开发注意事项

1. **资源管理**
   - 使用对象池管理内存（`imgBufPool`和`matPool`）
   - 及时释放FFmpeg进程和文件句柄
   - 控制并发会话数量

2. **错误处理**
   - 实现优雅退出机制
   - 处理FFmpeg进程异常
   - 自动重连机制

3. **性能优化**
   - 使用goroutine处理并发
   - 实现帧缓冲队列
   - 优化内存使用

4. **调试方法**
   ```bash
   # 查看日志
   tail -f logs/detectLog
   
   # 检查会话状态
   curl http://localhost:8080/api/v1/detect/session/{sessionId}
   
   # 监控系统资源
   docker stats ai-go-client
   ```

### 常见问题处理

1. **视频流断开**
   - 检查网络连接
   - 查看FFmpeg日志
   - 确认RTSP地址有效性

2. **检测服务异常**
   - 检查AI服务状态
   - 查看检测日志
   - 确认模型加载正常

3. **推流失败**
   - 检查RTMP服务器状态
   - 确认推流地址正确
   - 查看FFmpeg错误日志

4. **内存占用过高**
   - 检查会话数量
   - 查看对象池使用情况
   - 确认资源释放正常

### Session方法详解

#### 核心方法

1. **Run方法** - 会话主运行方法
   ```go
   func (s *Session) Run(aiDetectAIURL string, uvicornSocket bool, socketPath string, resultPath, resultPathReal string, detectStore DetectStore, pullRestart PullStreamEOFRestart)
   ```
   - **功能**：管理整个视频处理流程的主循环
   - **主要流程**：
     1. 初始化阶段
        - 创建结果目录
        - 初始化帧检测通道
        - 初始化图像缓冲区
        - 准备拉流器
     2. 启动检测协程
        - 异步启动`asyncDetectLoop`
        - 处理检测结果
     3. 主循环处理
        - 读取视频帧
        - 控制检测频率
        - 处理检测结果
        - 推流处理
     4. 错误处理
        - EOF处理
        - 自动重连
        - 资源清理
   - **关键特性**：
     - 支持动态帧率控制
     - 实现优雅退出
     - 自动重连机制
     - 资源自动回收

2. **asyncDetectLoop方法** - 异步检测循环
   ```go
   func (s *Session) asyncDetectLoop(uvicornSocket bool, socketPath string, aiDetectAIURL string, resultPath, resultPathReal string, detectStore DetectStore)
   ```
   - **功能**：异步处理视频帧检测
   - **工作流程**：
     1. 帧接收
        - 从`frameForDetection`通道接收视频帧
        - 支持两种检测模式：uvicorn socket和HTTP
     2. 目标检测
        - 调用AI服务进行检测
        - 处理检测结果
     3. 结果处理
        - 缓存新标签
        - 保存检测图片
        - 推送检测结果
   - **优化特性**：
     - 使用本地缓存避免重复存储
     - 异步处理检测结果
     - 支持批量处理
     - 错误重试机制

3. **StartRecording方法** - 录制控制
   ```go
   func (s *Session) StartRecording(recordDir, realDir string, segment time.Duration, detectStore DetectStore, pullRestart PullStreamEOFRestart) error
   ```
   - **功能**：管理视频录制流程
   - **实现细节**：
     1. 录制初始化
        - 创建录制目录
        - 设置录制状态
        - 启动异步录制协程
     2. 分段录制
        - 生成唯一文件名
        - 配置FFmpeg参数
        - 执行分段录制
     3. 结果处理
        - 保存录制文件
        - 推送录制信息
        - 处理录制异常
   - **FFmpeg参数优化**：
     ```go
     // 关键参数说明
     "-vf", "scale=640:360"     // 降为标清，节省空间
     "-c:v", "libx264"          // 使用H.264编码
     "-crf", "30"               // 高压缩率
     "-preset", "medium"        // 压缩率和速度平衡
     "-tune", "zerolatency"     // 低延迟优化
     "-pix_fmt", "yuv420p"      // 兼容性好的像素格式
     "-movflags", "+faststart"  // 支持快速播放
     ```

#### 辅助方法

1. **资源管理方法**
   ```go
   // 清理拉流资源
   func (s *Session) ClearPuller()
   
   // 清理推流资源
   func (s *Session) ClearPusher()
   
   // 清理录制资源
   func (s *Session) ClearRecorder()
   
   // 重置会话
   func (s *Session) Reset()
   ```
   - 负责各类资源的清理和重置
   - 确保资源正确释放
   - 防止资源泄漏

2. **状态控制方法**
   ```go
   // 更新RTSP地址
   func (s *Session) ResetRtspURL(rtspURL string) error
   
   // 停止录制
   func (s *Session) StopRecording()
   
   // 准备拉流器
   func (s *Session) PreparePuller() error
   
   // 准备推流器
   func (s *Session) PreParePusher(pushRTMPURL string) error
   ```
   - 管理会话状态转换
   - 控制视频流处理
   - 处理异常情况

3. **工具方法**
   ```go
   // 获取会话描述
   func (s *Session) GetDesc(pushUrlPublicPre, hlsPre string) SessionDesc
   
   // 发送关闭信号
   func (s *Session) SendIDToCloseCh()
   
   // 广播RTSP更新
   func (s *Session) BroadcastRtspUpdate(nowUnix int64, newURL string)
   ```
   - 提供会话信息查询
   - 处理会话生命周期
   - 管理状态通知

#### 错误处理机制

1. **进程异常处理**
   - 监控FFmpeg进程状态
   - 自动重连机制
   - 优雅退出处理

2. **资源异常处理**
   - 确保资源正确释放
   - 防止资源泄漏
   - 处理文件系统异常

3. **网络异常处理**
   - RTSP断流重连
   - 推流失败重试
   - 检测服务异常处理

#### 性能优化策略

1. **内存优化**
   - 使用对象池管理内存
   - 控制缓冲区大小
   - 及时释放资源

2. **并发优化**
   - 使用goroutine处理并发
   - 实现帧缓冲队列
   - 控制协程数量

3. **IO优化**
   - 使用缓冲IO
   - 批量处理数据
   - 控制处理速率

## 部署说明

### 本地构建

1. **构建镜像**
   ```bash
   # 进入项目根目录
   cd video_detect_v1
   
   # 构建基础服务镜像
   docker-compose -f ./build/docker-compose.yaml up --build
   
   # 构建AI服务镜像
   docker-compose -f ./build/docker-compose-ai.yaml up --build
   ```

2. **保存镜像**
   ```bash
   # 保存Go客户端镜像
   docker save -o build-go-client.tar build-go-client:latest
   
   # 保存RTMP服务器镜像
   docker save -o build-rtmp-server.tar build-rtmp-server:latest
   
   # 保存AI服务镜像
   docker save -o build-ai-service.tar build-ai-service:latest
   ```

### 服务器部署

1. **准备工作**
   ```bash
   # 在服务器上创建必要的目录（/data/irrigated/server/dist/upload 为项目部署的上传目录路径）
   mkdir -p /data/irrigated/server/dist/upload/recording
   mkdir -p /data/irrigated/server/dist/upload/detects
   mkdir -p /app/configs
   ```

2. **上传文件**
   - 上传镜像文件到服务器：
     ```bash
     # 使用scp或其他工具上传
     scp build-*.tar user@server:/path/to/destination/
     ```
   - 上传配置文件：
     ```bash
     # 上传docker-compose文件
     scp build/linux/docker-compos.yaml user@server:/path/to/destination/
     
     # 上传配置文件
     scp configs/video_detect_linux.toml user@server:/app/configs/
     ```

3. **加载镜像**
   ```bash
   # 加载所有镜像
   docker load -i build-go-client.tar
   docker load -i build-ai-service.tar
   docker load -i build-rtmp-server.tar
   ```

4. **启动服务**
   ```bash
   # 进入部署目录
   cd /path/to/destination
   
   # 启动所有服务
   docker-compose -f docker-compose.yaml up -d
   
   # 若遇到 下面错误
   
   rtmp-server is up-to-date
   Recreating ai-go-client ...
   
   ERROR: for ai-go-client  'ContainerConfig'
   
   ERROR: for go-client  'ContainerConfig'
   Traceback (most recent call last):
   File "docker-compose", line 3, in <module>
   File "compose/cli/main.py", line 81, in main
   File "compose/cli/main.py", line 203, in perform_command
   File "compose/metrics/decorator.py", line 18, in wrapper
   File "compose/cli/main.py", line 1186, in up
   File "compose/cli/main.py", line 1182, in up
   File "compose/project.py", line 702, in up
   File "compose/parallel.py", line 108, in parallel_execute
   File "compose/parallel.py", line 206, in producer
   File "compose/project.py", line 688, in do
   File "compose/service.py", line 581, in execute_convergence_plan
   File "compose/service.py", line 503, in _execute_convergence_recreate
   File "compose/parallel.py", line 108, in parallel_execute
   File "compose/parallel.py", line 206, in producer
   File "compose/service.py", line 496, in recreate
   File "compose/service.py", line 615, in recreate_container
   File "compose/service.py", line 334, in create_container
   File "compose/service.py", line 922, in _get_container_create_options
   File "compose/service.py", line 962, in _build_container_volume_options
   File "compose/service.py", line 1549, in merge_volume_bindings
   File "compose/service.py", line 1579, in get_container_data_volumes
   KeyError: 'ContainerConfig'
   
   执行下面命令后再次执行上面 docker-compose
   docker-compose down --volumes --remove-orphans 
   ```

### 服务验证

1. **检查服务状态**
   ```bash
   # 查看所有容器状态
   docker ps
   
   # 查看服务日志
   docker logs ai-go-client
   docker logs ai-service
   docker logs rtmp-server
   ```

2. **验证端口**
   ```bash
   # 检查端口监听状态
   netstat -tunlp | grep -E '7050|7051|7052|7059|5000'
   ```

3. **测试接口**
   ```bash
   # 测试HTTP接口
   curl http://localhost:7050/api/v1/detect/health
   
   # 测试RTMP推流
   ffmpeg -re -i test.mp4 -c copy -f flv rtmp://localhost:7059/live/test
   
   # 测试HLS播放
   curl http://localhost:7052/hls/test.m3u8
   ```

### 配置说明

1. **端口映射**
   - 7050: Go客户端HTTP服务
   - 7051: Go客户端gRPC服务
   - 7052: HLS播放服务
   - 7059: RTMP推流服务
   - 5000: AI服务接口

2. **目录挂载**
   ```yaml
   volumes:
     - ./configs:/app/configs:ro                    # 配置文件
     - /data/irrigated/server/dist/upload/recording:/app/recordings  # 录制文件
     - /data/irrigated/server/dist/upload/detects:/app/detects      # 检测结果
   ```

3. **网络配置**
   - 使用bridge网络模式
   - 服务间通过容器名互相访问
   - 外部通过映射端口访问

### 注意事项

1. **环境要求**
   - Docker 20.10+
   - Docker Compose 2.0+
   - 足够的磁盘空间（建议>100GB）
   - 建议使用GPU服务器运行AI服务

2. **安全建议**
   - 修改默认的API Token
   - 配置防火墙规则
   - 使用HTTPS访问
   - 定期备份数据

3. **维护建议**
   - 定期清理录制文件
   - 监控磁盘使用情况
   - 检查服务日志
   - 及时更新系统补丁

4. **常见问题**
   - 如果服务无法启动，检查端口占用
   - 如果推流失败，检查RTMP服务器状态
   - 如果检测不工作，检查AI服务日志
   - 如果存储空间不足，清理历史文件

5. **TODO 待优化项**
   - [ ] 系统资源检查
     - 在开启识别前检查系统资源（CPU、内存、GPU）使用情况
     - 设置资源使用阈值，超过阈值时拒绝开启新的识别任务
     - 实现资源监控和自动告警机制
     - 支持动态调整识别频率以平衡系统负载
     - 添加资源使用统计和预测功能
   - [ ] 其他优化项
     - 完善gRPC接口实现
     - 添加更多的单元测试
     - 优化日志记录策略
     - 实现更细粒度的权限控制
