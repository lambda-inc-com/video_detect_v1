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