### 以下由AI生成：

# API 使用说明

本文档演示如何使用本项目内嵌的 HTTP 异步任务接口从 Node.js 或命令行触发下载并接收文件。

## 启动服务

先编译并以 API 模式启动：

```bash
# 编译（可选）
go build -o main .

# 启动 API（阻塞，监听 :18080）
./main --api
# Windows: main.exe --api
```

默认 worker 数为 2；可通过环境变量 `API_WORKERS` 调整并发 worker 数量，例如：

```bash
export API_WORKERS=4
./main --api
```

## 异步 API（推荐）

主要端点：

- `POST /download`：提交下载任务（JSON body: `{ "url": "...", "output": "/optional/out/dir" }`），返回 HTTP 202 与 JSON：`{ id, status_url, result_url }`。
- `GET /status?id=...`：查询任务状态，返回任务对象（包含 `status` 字段，值为 `pending|running|done|error`）。
- `GET /result?id=...`：任务完成后返回任务结果元数据，包含 `result_dir` 和 `files`。如果使用 `output` 参数，则目录即为指定输出路径。

### curl 示例（提交并轮询）

```bash
# 提交任务
curl -s -X POST -H "Content-Type: application/json" \
  -d '{"url":"https://music.apple.com/us/album/..."}' \
  http://localhost:18080/download

# 假设返回 {"id":"a1b2c3","status_url":"/status?id=a1b2c3","result_url":"/result?id=a1b2c3"}
# 轮询状态
TASK_ID=a1b2c3
while true; do
  curl -s "http://localhost:18080/status?id=${TASK_ID}" | jq .
  sleep 5
done
```

如需在提交时指定输出目录（覆盖配置）可添加 `output` 字段：

```bash
curl -s -X POST -H "Content-Type: application/json" \
  -d '{"url":"https://music.apple.com/us/album/...","output":"/tmp/myout"}' \
  http://localhost:18080/download
```

## Node.js 示例（轮询并下载）

下面示例使用原生 `fetch`（Node 18+）。提交任务后轮询状态，完成后下载结果并保存为 `out.m4a`：

```javascript
import fs from 'fs';

const submit = async (url) => {
  const r = await fetch('http://localhost:18080/download', {
    method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify({ url })
  });
  if (!r.ok) throw new Error(await r.text());
  return await r.json(); // { id, status_url, result_url }
};

const pollUntilDone = async (id, interval = 3000) => {
  while (true) {
    const r = await fetch(`http://localhost:18080/status?id=${id}`);
    const json = await r.json();
    if (json.status === 'done') return;
    if (json.status === 'error') throw new Error(JSON.stringify(json));
    await new Promise(r => setTimeout(r, interval));
  }
};

const downloadResult = async (id, outPath) => {
  const r = await fetch(`http://localhost:18080/result?id=${id}`);
  if (!r.ok) throw new Error(await r.text());
  const w = fs.createWriteStream(outPath);
  await new Promise((res, rej) => {
    r.body.pipe(w);
    r.body.on('error', rej);
    w.on('finish', res);
  });
};

// usage
const info = await submit('https://music.apple.com/us/album/...');
console.log('task id', info.id);
await pollUntilDone(info.id);
await downloadResult(info.id, 'out.m4a');
console.log('saved out.m4a');
```

如果使用 `axios`，对 `/result` 使用 `responseType: 'stream'` 并把流写入文件即可。

## 注意事项

- API 使用异步任务队列并通过 worker 并发执行下载，worker 默认以子进程方式运行现有 CLI 下载逻辑，从而避免在同一进程中重入导致的竞态。可通过 `API_WORKERS`  调整并发数量。
- `POST /download` 立即返回任务 ID；请使用 `GET /status?id=...` 轮询，并在 `GET /result?id=...` 获取目录和文件列表。
- 如果提交时指定 `output`，下载结果会直接写入该目录；如果不指定 `output`，下载结果会写入临时目录并保留在 `result_dir` 中。
- 现阶段没有实现自动清理临时目录；如果你不想保留结果目录，请在 Node 端处理完成后手动删除。
- 若需认证、任务取消、持久化队列或更复杂的并发策略（优先级、配额等），我可以继续为你扩展这些功能。
