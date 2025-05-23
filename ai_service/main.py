# main.py
from fastapi import FastAPI, Request
from fastapi.responses import JSONResponse
import subprocess
import numpy as np, cv2
from ultralytics import YOLO
import psutil

app = FastAPI()
model = YOLO("yolov8n.pt")

allowed_labels = [
    "person", "car", "bus", "truck", "bicycle", "motorcycle",
    "dog", "cat", "cow", "sheep", "horse",
    "fire hydrant", "backpack", "handbag", "stop sign", "traffic light"
]

@app.post("/detect")
async def detect(request: Request):
    try:
        data = await request.body()
        if not data:
            return JSONResponse(content={"error": "未收到图像数据"})

        img_np = np.frombuffer(data, np.uint8)
        frame = cv2.imdecode(img_np, cv2.IMREAD_COLOR)
        if frame is None:
            return JSONResponse(content={"error": "图像解码失败"})

        # 推理
        results = model(frame, conf=0.5)[0]  # 设定默认置信度阈值 0.5

        # 处理结果
        output = []
        for box in results.boxes:
            cls_id = int(box.cls[0])
            label = model.names[cls_id]
            if label not in allowed_labels:
                continue
            x1, y1, x2, y2 = map(int, box.xyxy[0])
            conf = float(box.conf[0])
            output.append({
                "x1": x1, "y1": y1,
                "x2": x2, "y2": y2,
                "label": label,
                "conf": conf
            })

        # 返回结构更完整
        response = {
            "success": True,
            "count": len(output),
            "results": output,
        }
        return JSONResponse(content=response)

    except Exception as e:
        return JSONResponse(content={"error": f"服务器内部错误: {str(e)}"})


@app.get("/status")
async def status():
    # 获取 CPU 使用率
    cpu_percent = psutil.cpu_percent(interval=0.5)
    # CPU 核心数
    cpu_count = psutil.cpu_count(logical=True)

    # 获取内存使用情况
    mem = psutil.virtual_memory()
    mem_total = mem.total // (1024 * 1024)
    mem_used = mem.used // (1024 * 1024)
    mem_percent = mem.percent

    # 检查 GPU 是否可用
    has_gpu = False
    gpu_info = {}
    try:
        gpu_output = subprocess.check_output(
            ["nvidia-smi", "--query-gpu=utilization.gpu,memory.used,memory.total", "--format=csv,noheader,nounits"],
            stderr=subprocess.DEVNULL
        ).decode().strip()
        gpu_util, mem_used_gpu, mem_total_gpu = gpu_output.split(", ")
        gpu_info = {
            "gpu_utilization_percent": int(gpu_util),
            "gpu_memory_used_mb": int(mem_used_gpu),
            "gpu_memory_total_mb": int(mem_total_gpu),
        }
        has_gpu = True
    except Exception:
        gpu_info = {}

    # 返回结构统一
    response = {
        "success": True,
        "cpu_percent": cpu_percent,
        "cpu_count": cpu_count, 
        "memory": {
            "total_mb": mem_total,
            "used_mb": mem_used,
            "percent": mem_percent
        },
        "has_gpu": has_gpu,
        "gpu": gpu_info
    }
    return JSONResponse(content=response)


# 读取宿主机 CPU 核心数
def get_host_cpu_count():
    with open("/host_proc/cpuinfo") as f:
        return sum(1 for line in f if line.strip().startswith("processor"))

# 读取宿主机内存信息
def get_host_memory():
    meminfo = {}
    with open("/host_proc/meminfo") as f:
        for line in f:
            parts = line.split()
            if len(parts) >= 2:
                meminfo[parts[0].rstrip(":")] = int(parts[1])
    total_kb = meminfo.get("MemTotal", 0)
    available_kb = meminfo.get("MemAvailable", 0)
    used_kb = total_kb - available_kb
    percent = round(used_kb / total_kb * 100, 2) if total_kb > 0 else 0.0
    return {
        "total_mb": total_kb // 1024,
        "used_mb": used_kb // 1024,
        "percent": percent
    }

# GPU 检测
def get_host_gpu_info():
    try:
        gpu_output = subprocess.check_output(
            ["nvidia-smi", "--query-gpu=utilization.gpu,memory.used,memory.total", "--format=csv,noheader,nounits"],
            stderr=subprocess.DEVNULL
        ).decode().strip()
        gpu_util, mem_used_gpu, mem_total_gpu = gpu_output.split(", ")
        return True, {
            "gpu_utilization_percent": int(gpu_util),
            "gpu_memory_used_mb": int(mem_used_gpu),
            "gpu_memory_total_mb": int(mem_total_gpu),
        }
    except Exception:
        return False, {}

def get_host_cpu_usage_instant():
    with open("/host_proc/stat") as f:
        cpu_line = f.readline()
    fields = [float(x) for x in cpu_line.strip().split()[1:]]
    total = sum(fields)
    idle = fields[3]  # idle time
    iowait = fields[4]  # I/O wait time
    non_idle = total - idle - iowait  # active CPU time
    if total == 0:
        return 0.0
    return round(non_idle / total * 100, 2)

@app.get("/host/status")
async def host_status():
    try:
        cpu_percent = get_host_cpu_usage_instant()  # ✅ 使用缓存，立即返回
        cpu_count = get_host_cpu_count()
        memory = get_host_memory()
        has_gpu, gpu_info = get_host_gpu_info()

        response = {
            "success": True,
            "cpu_percent": cpu_percent,
            "cpu_count": cpu_count,
            "memory": memory,
            "has_gpu": has_gpu,
            "gpu": gpu_info
        }
    except Exception as e:
        response = {
            "success": False,
            "error": str(e)
        }
    return JSONResponse(content=response)