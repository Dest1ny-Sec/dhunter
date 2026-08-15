# 自定义工具

平台自带 20 个原创渗透工具（编译内置）。你还可以**不用改代码**就添加自己的工具：

把任意 `.yaml` 文件放进这个目录（`tools/custom/`），重启 dhunter-mcp 即生效。

## 格式

```yaml
- name: nmap_scan              # 工具名（agent 会用这个名字调用）
  description: "对目标运行 nmap TCP 扫描"   # 给 AI 看的说明
  input_schema:                # 参数定义（JSON Schema）
    type: object
    properties:
      target: { type: string }
      ports:  { type: string }
    required: [target]
  command: "nmap -sV -Pn {target} -p {ports}"   # 命令模板，{参数} 会被替换
  timeout: 600                  # 超时（秒），默认 120
```

- 一个文件可定义多个工具（YAML 数组）
- `{参数名}` 占位符会被替换为传入的参数值（自动安全转义）
- 目录路径可用环境变量 `DHUNTER_CUSTOM_TOOLS_DIR` 覆盖
- 命令走系统 shell（macOS/Linux 用 `sh -c`，Windows 用 `cmd /C`），输出和超时都受限（与内置工具一致）

## 示例

```yaml
- name: nmap_scan
  description: "对目标运行 nmap TCP 扫描（-sV 版本探测）"
  input_schema:
    type: object
    properties:
      target: { type: string, description: "IP 或域名" }
      ports:  { type: string, description: "端口，如 80,443,8080" }
    required: [target]
  command: "nmap -sV -Pn {target} -p {ports}"
  timeout: 600

- name: curl_header
  description: "用 curl 查看响应头"
  input_schema:
    type: object
    properties:
      url: { type: string }
    required: [url]
  command: "curl -sI {url}"
  timeout: 30
```
